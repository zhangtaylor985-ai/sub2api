package sessiondelivery

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Both shapes were measured in captured tool output.
func TestRedactSecretsMasksMeasuredCredentials(t *testing.T) {
	document := []byte(`{"text":"config/prod.yml:173: api_key: sk-4ebf35ad062c40a88a3799c3c9ce3e28",` +
		`"cmd":"--header 'authorization: Bearer 1124b7dd-14c7-4e4b-8655-8d79b757a7f1.44dd1a9e8a'"}`)
	require.Error(t, validateNoSecrets(document))

	redacted, count := redactSecrets(document)
	require.Equal(t, int64(2), count)
	require.NoError(t, validateNoSecrets(redacted))

	got := string(redacted)
	require.NotContains(t, got, "sk-4ebf35ad062c40a88a3799c3c9ce3e28")
	require.NotContains(t, got, "1124b7dd-14c7-4e4b-8655-8d79b757a7f1.44dd1a9e8a")
	// The surrounding document is untouched, so the record still reads as the
	// config file and the curl command it was.
	require.Contains(t, got, "config/prod.yml:173: api_key: sk-REDACTED-")
	require.Contains(t, got, "--header 'authorization: Bearer REDACTED-")
}

// A credential quoted in several places has to come out the same every time,
// or a conversation about it stops making sense.
func TestRedactSecretsIsStableAcrossOccurrencesAndDocuments(t *testing.T) {
	secret := "sk-N8aBsZeVOnYUME182IT6ea1If7q6L9Idh9al3QV5ZgVveXyh"
	first, count := redactSecrets([]byte("a " + secret + " b " + secret))
	require.Equal(t, int64(2), count)

	labels := strings.Fields(strings.ReplaceAll(string(first), "a ", ""))
	require.Equal(t, labels[0], labels[2], "the same secret must mask to the same label")

	// The same secret in a different document masks identically, so the request
	// and the response still agree.
	second, _ := redactSecrets([]byte("elsewhere: " + secret))
	require.Contains(t, string(second), labels[0])
}

// Two different credentials must stay distinguishable.
func TestRedactSecretsKeepsDistinctCredentialsDistinct(t *testing.T) {
	redacted, count := redactSecrets([]byte(
		"dev: sk-4909cc2c34764469af5cf2405c626bec prod: sk-4ebf35ad062c40a88a3799c3c9ce3e28"))
	require.Equal(t, int64(2), count)
	fields := strings.Fields(string(redacted))
	require.NotEqual(t, fields[1], fields[3])
}

// The corpus carries 660 matches for a loose key/token assignment pattern and
// every one is ordinary code. Redaction must not touch any of them.
func TestRedactSecretsLeavesIdentifiersAndSlugsAlone(t *testing.T) {
	for _, text := range []string{
		"state_tokens_before_call = self._count_tokens(state_messages)",
		"llm_messages_tokens = self._count_tokens(llm_messages)",
		"api_key: sk-chat-v2-frontend-api",
		"model: sk-prediction-using-ai-derived-epi",
		"session_id: 1124b7dd-14c7-4e4b-8655-8d79b757a7f1",
		"token_usage.py:122: system_tokens = self._count_tokens(system_message)",
		"password = os.environ[\"DB_PASSWORD\"]",
	} {
		redacted, count := redactSecrets([]byte(text))
		require.Zero(t, count, text)
		require.Equal(t, text, string(redacted))
		require.NoError(t, validateNoSecrets([]byte(text)))
	}
}

func TestRedactSecretsIsIdempotent(t *testing.T) {
	document := []byte(`api_key: sk-4ebf35ad062c40a88a3799c3c9ce3e28`)
	first, count := redactSecrets(document)
	require.Equal(t, int64(1), count)

	second, again := redactSecrets(first)
	require.Zero(t, again)
	require.Equal(t, string(first), string(second))
}

// End to end: a credential in the response has to be masked too, or masking it
// in the request accomplishes nothing.
func TestNormalizeProjectionFidelityRedactsRequestAndResponse(t *testing.T) {
	secret := "sk-4ebf35ad062c40a88a3799c3c9ce3e28"
	request := mustJSON(map[string]any{
		"model":      DefaultPublicModel,
		"max_tokens": 64000,
		"thinking":   map[string]any{"type": "adaptive", "display": "omitted"},
		"messages": []any{map[string]any{
			"role":    "user",
			"content": []any{map[string]any{"type": "text", "text": "api_key: " + secret}},
		}},
	})
	response := mustJSON(map[string]any{
		"id":    "msg_01" + "abcdefghijklmnopqrstuv",
		"type":  "message",
		"role":  "assistant",
		"model": DefaultPublicModel,
		"content": []any{map[string]any{
			"type": "text", "text": "你的 key 是 " + secret + "，建议轮换。",
		}},
		"stop_reason": "end_turn",
		"usage":       map[string]any{"input_tokens": 10, "output_tokens": 20},
	})

	normalizedRequest, normalizedResponse, stats, err := normalizeProjectionFidelity(
		request, response, fidelityNormalizationOptions{},
	)
	require.NoError(t, err)
	require.Equal(t, int64(2), stats.SecretsRedacted)
	require.NoError(t, validateNoSecrets(normalizedRequest, normalizedResponse))
	require.NotContains(t, string(normalizedRequest), secret)
	require.NotContains(t, string(normalizedResponse), secret)

	// Request and response must land on the same label or the exchange stops
	// referring to one credential.
	var decoded map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(normalizedResponse, &decoded))
	label := extractRedactionLabel(t, string(normalizedRequest))
	require.Contains(t, string(normalizedResponse), label)
}

func extractRedactionLabel(t *testing.T, document string) string {
	t.Helper()
	index := strings.Index(document, "sk-REDACTED-")
	require.Positive(t, index)
	return document[index : index+len("sk-REDACTED-")+8]
}
