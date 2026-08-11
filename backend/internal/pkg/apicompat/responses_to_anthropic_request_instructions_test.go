package apicompat

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestResponsesToAnthropicRequestPreservesInstructionsAndDeveloperMessages(t *testing.T) {
	request := &ResponsesRequest{
		Model:        "gpt-5.6-sol",
		Instructions: "Top-level instructions",
		Input: json.RawMessage(`[
			{"role":"developer","content":[{"type":"input_text","text":"Developer rules"}]},
			{"role":"system","content":"System rules"},
			{"role":"user","content":"Hello"}
		]`),
	}
	converted, err := ResponsesToAnthropicRequest(request)
	require.NoError(t, err)
	var system string
	require.NoError(t, json.Unmarshal(converted.System, &system))
	require.Equal(t, "Top-level instructions\n\nDeveloper rules\n\nSystem rules", system)
	require.Len(t, converted.Messages, 1)
	require.Equal(t, "user", converted.Messages[0].Role)
}
