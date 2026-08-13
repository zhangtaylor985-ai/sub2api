package sessiondelivery

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func echoTestRecord(sessionID, requestText, priorAssistantText string, thinkingEnabled bool, respText, respSignature string) *DeliveryRecord {
	thinking := ""
	if thinkingEnabled {
		thinking = `,"thinking":{"type":"adaptive"}`
	}
	messages := `[{"role":"user","content":[{"type":"text","text":` + mustJSONString(requestText) + `}]}]`
	if priorAssistantText != "" {
		messages = `[{"role":"user","content":[{"type":"text","text":"first"}]},` +
			`{"role":"assistant","content":[{"type":"text","text":` + mustJSONString(priorAssistantText) + `}]},` +
			`{"role":"user","content":[{"type":"text","text":` + mustJSONString(requestText) + `}]}]`
	}
	request := json.RawMessage(`{"model":"claude-opus-5","max_tokens":1024` + thinking + `,"messages":` + messages + `}`)

	content := `[{"type":"thinking","thinking":"","signature":` + mustJSONString(respSignature) + `},{"type":"text","text":` + mustJSONString(respText) + `}]`
	if respSignature == "" {
		content = `[{"type":"text","text":` + mustJSONString(respText) + `}]`
	}
	response := json.RawMessage(`{"id":"msg_1","type":"message","role":"assistant","model":"claude-opus-5","content":` + content + `,"stop_reason":"end_turn"}`)

	return &DeliveryRecord{
		SessionID: sessionID,
		RequestID: "req_" + sessionID + requestText,
		Timestamp: time.Now().UTC(),
		Metadata:  DeliveryMetadata{HTTPStatus: 200, LatencyMS: 5},
		Request:   request,
		Response:  DeliveryResponse{StatusCode: 200, ResponseData: response},
	}
}

func mustJSONString(v string) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}

func requestAssistantThinkingSignature(t *testing.T, record *DeliveryRecord) string {
	t.Helper()
	var request struct {
		Messages []struct {
			Role    string `json:"role"`
			Content []struct {
				Type      string `json:"type"`
				Signature string `json:"signature"`
			} `json:"content"`
		} `json:"messages"`
	}
	require.NoError(t, json.Unmarshal(record.Request, &request))
	for _, msg := range request.Messages {
		if msg.Role != "assistant" {
			continue
		}
		for _, block := range msg.Content {
			if block.Type == "thinking" {
				return block.Signature
			}
		}
	}
	return ""
}

func TestEchoRepairInsertsPriorThinkingIntoLaterRequests(t *testing.T) {
	repair := &echoRepair{}

	turn1 := echoTestRecord("session_a", "first question", "", true, "answer one", "sig-AAA")
	require.NoError(t, repair.process(turn1))
	// first turn has no prior response to echo
	require.NotContains(t, string(turn1.Request), "sig-AAA")

	turn2 := echoTestRecord("session_a", "second question", "answer one", true, "answer two", "sig-BBB")
	require.NoError(t, repair.process(turn2))
	// turn2 request history now echoes turn1's thinking block verbatim
	require.Equal(t, "sig-AAA", requestAssistantThinkingSignature(t, turn2))
	require.NoError(t, ValidateDelivery(turn2, DefaultPublicModel))

	// turn2's own response is collected for later turns
	turn3 := echoTestRecord("session_a", "third question", "answer two", true, "answer three", "sig-CCC")
	require.NoError(t, repair.process(turn3))
	require.Equal(t, "sig-BBB", requestAssistantThinkingSignature(t, turn3))
}

func TestEchoRepairSkipsWhenThinkingNotEnabled(t *testing.T) {
	repair := &echoRepair{}
	turn1 := echoTestRecord("session_b", "q1", "", true, "answer one", "sig-AAA")
	require.NoError(t, repair.process(turn1))

	turn2 := echoTestRecord("session_b", "q2", "answer one", false, "answer two", "sig-BBB")
	require.NoError(t, repair.process(turn2))
	require.Empty(t, requestAssistantThinkingSignature(t, turn2))
}

func TestEchoRepairLeavesUnmatchedHistoryAlone(t *testing.T) {
	repair := &echoRepair{}
	turn1 := echoTestRecord("session_c", "q1", "", true, "answer one", "sig-AAA")
	require.NoError(t, repair.process(turn1))

	// assistant history text does not match any prior response (compaction etc.)
	turn2 := echoTestRecord("session_c", "q2", "some other text", true, "answer two", "sig-BBB")
	require.NoError(t, repair.process(turn2))
	require.Empty(t, requestAssistantThinkingSignature(t, turn2))
}

func TestEchoRepairDoesNotCrossSessions(t *testing.T) {
	repair := &echoRepair{}
	turn1 := echoTestRecord("session_d", "q1", "", true, "answer one", "sig-AAA")
	require.NoError(t, repair.process(turn1))

	other := echoTestRecord("session_e", "q2", "answer one", true, "answer two", "sig-BBB")
	require.NoError(t, repair.process(other))
	require.Empty(t, requestAssistantThinkingSignature(t, other))
}

func TestEchoRepairAlignsDuplicateAssistantTextsByTurn(t *testing.T) {
	repair := &echoRepair{}
	turn1 := echoTestRecord("session_duplicate", "q1", "", true, "same answer", "sig-AAA")
	require.NoError(t, repair.process(turn1))
	turn2 := echoTestRecord("session_duplicate", "q2", "same answer", true, "same answer", "sig-BBB")
	require.NoError(t, repair.process(turn2))

	turn3 := echoTestRecord("session_duplicate", "q3", "", true, "done", "sig-CCC")
	turn3.Request = json.RawMessage(`{
		"model":"claude-opus-5","max_tokens":1024,"thinking":{"type":"adaptive"},
		"messages":[
			{"role":"user","content":[{"type":"text","text":"q1"}]},
			{"role":"assistant","content":[{"type":"text","text":"same answer"}]},
			{"role":"user","content":[{"type":"text","text":"q2"}]},
			{"role":"assistant","content":[{"type":"text","text":"same answer"}]},
			{"role":"user","content":[{"type":"text","text":"q3"}]}
		]
	}`)
	require.NoError(t, repair.process(turn3))

	var request struct {
		Messages []struct {
			Role    string `json:"role"`
			Content []struct {
				Type      string `json:"type"`
				Signature string `json:"signature"`
			} `json:"content"`
		} `json:"messages"`
	}
	require.NoError(t, json.Unmarshal(turn3.Request, &request))
	var signatures []string
	for _, message := range request.Messages {
		if message.Role != "assistant" {
			continue
		}
		for _, block := range message.Content {
			if block.Type == "thinking" {
				signatures = append(signatures, block.Signature)
			}
		}
	}
	require.Equal(t, []string{"sig-AAA", "sig-BBB"}, signatures)
}

func TestAssistantContentKeySupportsToolOnlyTurns(t *testing.T) {
	responseContent := []json.RawMessage{
		json.RawMessage(`{"type":"thinking","thinking":"","signature":"sig"}`),
		json.RawMessage(`{"type":"tool_use","id":"toolu_1","name":"Read","input":{"file_path":"/tmp/a"}}`),
	}
	requestContent := []json.RawMessage{
		json.RawMessage(`{"input":{"file_path":"/tmp/a"},"name":"Read","id":"toolu_1","type":"tool_use"}`),
	}
	require.NotEmpty(t, assistantContentKey(responseContent))
	require.Equal(t, assistantContentKey(responseContent), assistantContentKey(requestContent))
}
