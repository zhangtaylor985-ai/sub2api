package sessiondelivery

import "encoding/json"

// countUndeclaredResponseTools reports a response that calls a tool the request
// never offered.
//
// A request carrying no tools at all cannot produce a tool call: the model was
// given nothing to call. Such a pairing is a projection artifact, and it cannot
// be repaired from what the record contains — renaming the call would still
// leave it undeclared, and inventing a tools array would fabricate a surface
// the client never sent. MEASURED: 10 of 10365 captured records, every one with
// the request.tools key absent, calling names the conversion had no declaration
// to map from.
//
// The narrower condition is deliberate. A response naming a tool that is absent
// from a non-empty tools array does occur in genuine Claude Code traffic, so
// only the case with no declared surface whatsoever is treated as unrepairable.
func countUndeclaredResponseTools(request, response map[string]json.RawMessage) int64 {
	if isJSONArray(request["tools"]) {
		var tools []json.RawMessage
		if err := json.Unmarshal(request["tools"], &tools); err == nil && len(tools) > 0 {
			return 0
		}
	}
	var content []map[string]json.RawMessage
	if err := json.Unmarshal(response["content"], &content); err != nil {
		return 0
	}
	var calls int64
	for _, block := range content {
		if rawString(block["type"]) == "tool_use" {
			calls++
		}
	}
	return calls
}
