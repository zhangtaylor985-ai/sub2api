package sessiondelivery

import (
	"embed"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
)

// claudeCodeToolData holds tool definitions lifted verbatim from real Claude
// Code requests in this corpus, so a converted tool carries the client's own
// description and input schema rather than a hand-written imitation. Only the
// most frequent variant of each definition is kept, and every variant that
// embedded a user's own paths or settings was discarded during extraction.
//
//go:embed claude_code_tools.json
var claudeCodeToolData embed.FS

var loadClaudeCodeTools = sync.OnceValues(func() (map[string]json.RawMessage, error) {
	raw, err := claudeCodeToolData.ReadFile("claude_code_tools.json")
	if err != nil {
		return nil, fmt.Errorf("read embedded Claude Code tool definitions: %w", err)
	}
	var tools map[string]json.RawMessage
	if err := json.Unmarshal(raw, &tools); err != nil {
		return nil, fmt.Errorf("decode embedded Claude Code tool definitions: %w", err)
	}
	return tools, nil
})

// foreignToolTargets maps a tool declared by another client onto the Claude Code
// tool that does the same job. The pairing is by function, read from each
// foreign tool's own description, not by name similarity.
var foreignToolTargets = map[string]string{
	"exec":          "Bash",
	"exec_command":  "Bash",
	"shell_command": "Bash",
	// Claude Code's Read covers image files, which is all view_image does.
	"read":        "Read",
	"view_image":  "Read",
	"write":       "Write",
	"edit":        "Edit",
	"apply_patch": "Edit",
	"update_plan": "TaskUpdate",
	"get_goal":    "TaskGet",
	"create_goal": "TaskCreate",
	"update_goal": "TaskUpdate",
	// Direct counterparts.
	"request_user_input":          "AskUserQuestion",
	"list_mcp_resources":          "ListMcpResourcesTool",
	"read_mcp_resource":           "ReadMcpResourceTool",
	"list_mcp_resource_templates": "ReadMcpResourceDirTool",
	"agents_list":                 "ListAgents",
	"message":                     "SendMessage",
	"sessions_send":               "SendMessage",
}

// foreignArgumentTargets renames a converted call's arguments so its input
// matches the Claude Code schema of the tool it was mapped onto. A Claude Code
// call carrying a field its own schema does not define is a fingerprint of its
// own, so an argument that survives has to land on a real field.
//
// The "input" key is how the Codex projection wraps a freeform custom tool's
// text, which for a shell tool is the command itself.
var foreignArgumentTargets = map[string]map[string]string{
	"Read": {
		"path":   "file_path",
		"offset": "offset",
		"limit":  "limit",
	},
	"Write": {
		"path":    "file_path",
		"content": "content",
	},
	"Edit": {"path": "file_path"},
	"Bash": {
		"cmd":        "command",
		"command":    "command",
		"input":      "command",
		"timeout":    "timeout",
		"timeout_ms": "timeout",
		"yieldMs":    "timeout",
	},
}

// foreignArgumentDroppable lists the arguments that may be dropped when a call
// is converted, because they tell the originating client how to run the tool
// without changing the result it produced.
//
// Anything not listed here and not renamed above is an error rather than a
// silent drop: an argument like a working directory or an environment override
// changes what the call actually did, so losing it would misrepresent the
// exchange. convertBashWorkdir shows the alternative — express the argument the
// way Claude Code would, and keep it.
var foreignArgumentDroppable = map[string]map[string]bool{
	"Read": {"detail": true},
	"Bash": {
		"justification":       true,
		"login":               true,
		"max_output_tokens":   true,
		"prefix_rule":         true,
		"sandbox_permissions": true,
		"shell":               true,
		"tty":                 true,
		"workdir":             true, // folded into the command first.
	},
}

// foreignToolConversionStats records what the conversion did, so a rebuild can
// report it and a test can assert it.
type foreignToolConversionStats struct {
	ToolsConverted int
	ToolsDropped   int
	CallsConverted int
	// SystemPromptTools counts the converted-away tools the system prompt still
	// names. Conversion reaches the tool declarations and the calls, but not the
	// prompt, so a non-zero count means the record now instructs the model to
	// call a tool surface it no longer declares. Such a record cannot be
	// delivered consistently and is held back by the caller.
	SystemPromptTools int
}

func (s foreignToolConversionStats) changed() bool {
	return s.ToolsConverted > 0 || s.ToolsDropped > 0 || s.CallsConverted > 0
}

// convertForeignClientTools rewrites a request declared by another client into
// the Claude Code tool surface.
//
// The delivery presents every record as a Claude Code exchange, so a record
// whose tools come from a different client has to be converted rather than
// shipped as it stands: the name, description and input schema all identify the
// originating client, and several descriptions name the vendor outright.
//
// Three treatments, in order of preference:
//
//   - A tool with a Claude Code counterpart is replaced by that counterpart's
//     real definition, and every call to it is renamed with its arguments
//     mapped onto the target schema.
//   - A namespaced tool (ns__tool) becomes an MCP tool (mcp__ns__tool), which is
//     how Claude Code carries externally provided tools. This covers the
//     namespaced Codex projection without inventing a counterpart for it.
//   - A tool with no counterpart that the exchange never called is dropped. It
//     advertised a capability the assistant never used, so dropping it removes a
//     client fingerprint without touching anything the model actually did.
//
// A tool with no counterpart that *was* called is an error: mangling a call the
// model really made would corrupt the exchange, so the honest response is to
// surface it and extend the mapping above.
//
// MEASURED in this corpus: 26 of 209 records declare foreign tools, in three
// cohorts — a Codex CLI set (apply_patch, update_plan, shell_command,
// codex_app, plus a tool with an empty name, which the Messages API rejects
// outright), a Codex exec set (exec_command, write_stdin, view_image) and one
// unrelated agent platform (browser, canvas, music_generate, tts). Of the 26
// foreign tools those records declare, only two were ever called: read and exec.
func convertForeignClientTools(
	request map[string]json.RawMessage,
	response map[string]json.RawMessage,
) (foreignToolConversionStats, error) {
	var stats foreignToolConversionStats
	if !isJSONArray(request["tools"]) {
		return stats, nil
	}
	var declared []map[string]json.RawMessage
	if err := json.Unmarshal(request["tools"], &declared); err != nil {
		return stats, fmt.Errorf("decode request tools for foreign client conversion: %w", err)
	}
	if len(declared) == 0 {
		return stats, nil
	}
	called, err := collectCalledToolNames(request, response)
	if err != nil {
		return stats, err
	}
	renames := make(map[string]string, len(declared))
	converted := make([]json.RawMessage, 0, len(declared))
	emitted := make(map[string]bool, len(declared))
	handled := make([]string, 0, len(declared))
	for _, tool := range declared {
		name := rawString(tool["name"])
		if isClaudeCodeToolName(name) {
			if !emitted[name] {
				emitted[name] = true
				encoded, marshalErr := json.Marshal(tool)
				if marshalErr != nil {
					return stats, fmt.Errorf("re-encode declared tool %q: %w", name, marshalErr)
				}
				converted = append(converted, encoded)
			}
			continue
		}
		target, definition, resolveErr := resolveForeignTool(name, tool)
		if resolveErr != nil {
			// Dropping is only safe for a capability the exchange never
			// exercised; otherwise a call would be left pointing at nothing.
			if called[name] {
				return stats, resolveErr
			}
			stats.ToolsDropped++
			handled = append(handled, name)
			continue
		}
		renames[name] = target
		stats.ToolsConverted++
		handled = append(handled, name)
		if emitted[target] {
			// Two foreign tools can share one counterpart — update_plan and
			// update_goal both update a task. The declaration is emitted once;
			// both call sites still rename onto it.
			continue
		}
		emitted[target] = true
		converted = append(converted, definition)
	}
	if !stats.changed() {
		return stats, nil
	}
	encoded, err := json.Marshal(converted)
	if err != nil {
		return stats, fmt.Errorf("re-encode converted tools: %w", err)
	}
	request["tools"] = encoded
	historyCalls, err := renameHistoryToolCalls(request, renames)
	if err != nil {
		return stats, err
	}
	responseCalls, err := renameResponseToolCalls(response, renames)
	if err != nil {
		return stats, err
	}
	stats.CallsConverted = historyCalls + responseCalls
	stats.SystemPromptTools = countSystemPromptToolMentions(request["system"], handled)
	return stats, nil
}

// countSystemPromptToolMentions reports how many of the converted-away tools the
// system prompt still names.
//
// Only names carrying an underscore are counted. A single-word tool name like
// "read" or "exec" is an ordinary English word, so matching it in prose would
// flag prompts that never referenced a tool at all; an underscored name is
// unambiguous. In this corpus the one affected prompt names seven of them
// (sessions_spawn, music_generate, file_fetch and so on), so the narrower test
// is enough to identify the record.
func countSystemPromptToolMentions(system json.RawMessage, handled []string) int {
	if len(system) == 0 || len(handled) == 0 {
		return 0
	}
	prompt := string(system)
	mentions := 0
	for _, name := range handled {
		if !strings.Contains(name, "__") && !strings.Contains(name, "_") {
			continue
		}
		if strings.Contains(prompt, name) {
			mentions++
		}
	}
	return mentions
}

// isClaudeCodeToolName reports whether a declared name is already part of the
// Claude Code tool surface, either as a built-in or as an MCP tool.
func isClaudeCodeToolName(name string) bool {
	if name == "" {
		return false
	}
	if strings.HasPrefix(name, mcpToolPrefix) {
		return true
	}
	_, known := claudeCodeToolNames[name]
	return known
}

// resolveForeignTool returns the Claude Code declaration a foreign tool becomes.
func resolveForeignTool(name string, tool map[string]json.RawMessage) (string, json.RawMessage, error) {
	if target, ok := foreignToolTargets[name]; ok {
		tools, err := loadClaudeCodeTools()
		if err != nil {
			return "", nil, err
		}
		definition, ok := tools[target]
		if !ok {
			return "", nil, fmt.Errorf("no embedded Claude Code definition for tool %q", target)
		}
		return target, definition, nil
	}
	// A namespaced tool is already shaped like an MCP tool, so it only needs the
	// prefix that marks it as externally provided. Its own description and
	// schema stay, because they describe the tool truthfully.
	namespaced, ok := mcpNamespacedToolName(name)
	if !ok {
		return "", nil, fmt.Errorf("tool %q has no Claude Code counterpart", name)
	}
	renamed := make(map[string]json.RawMessage, len(tool))
	for key, value := range tool {
		renamed[key] = value
	}
	renamed["name"] = mustJSON(namespaced)
	definition, err := json.Marshal(renamed)
	if err != nil {
		return "", nil, fmt.Errorf("re-encode namespaced tool %q: %w", name, err)
	}
	return namespaced, definition, nil
}

// mcpNamespacedToolName converts ns__tool into Claude Code's mcp__ns__tool.
func mcpNamespacedToolName(name string) (string, bool) {
	namespace, tool, ok := strings.Cut(name, "__")
	if !ok || namespace == "" || tool == "" {
		return "", false
	}
	return mcpToolPrefix + name, true
}

// collectCalledToolNames reports which tools the exchange actually called, in
// the request history and in the response. A called tool may never be dropped,
// so both sides have to be counted before any declaration is removed.
func collectCalledToolNames(
	request map[string]json.RawMessage,
	response map[string]json.RawMessage,
) (map[string]bool, error) {
	called := make(map[string]bool)
	if isJSONArray(request["messages"]) {
		var messages []map[string]json.RawMessage
		if err := json.Unmarshal(request["messages"], &messages); err != nil {
			return nil, fmt.Errorf("decode request messages for foreign tool conversion: %w", err)
		}
		for _, message := range messages {
			if err := collectContentToolNames(message["content"], called); err != nil {
				return nil, err
			}
		}
	}
	if response != nil {
		if err := collectContentToolNames(response["content"], called); err != nil {
			return nil, err
		}
	}
	return called, nil
}

func collectContentToolNames(content json.RawMessage, called map[string]bool) error {
	if !isJSONArray(content) {
		return nil
	}
	var blocks []map[string]json.RawMessage
	if err := json.Unmarshal(content, &blocks); err != nil {
		return fmt.Errorf("decode content blocks for foreign tool conversion: %w", err)
	}
	for _, block := range blocks {
		if !isToolCallBlock(rawString(block["type"])) {
			continue
		}
		called[rawString(block["name"])] = true
	}
	return nil
}

func isToolCallBlock(blockType string) bool {
	return blockType == "tool_use" || blockType == "server_tool_use"
}

// renameHistoryToolCalls applies the renames to every call in the request
// history.
func renameHistoryToolCalls(request map[string]json.RawMessage, renames map[string]string) (int, error) {
	if len(renames) == 0 || !isJSONArray(request["messages"]) {
		return 0, nil
	}
	var messages []map[string]json.RawMessage
	if err := json.Unmarshal(request["messages"], &messages); err != nil {
		return 0, fmt.Errorf("decode request messages for tool call renaming: %w", err)
	}
	converted := 0
	for _, message := range messages {
		renamed, count, err := renameContentToolCalls(message["content"], renames)
		if err != nil {
			return 0, err
		}
		if count == 0 {
			continue
		}
		message["content"] = renamed
		converted += count
	}
	if converted == 0 {
		return 0, nil
	}
	encoded, err := json.Marshal(messages)
	if err != nil {
		return 0, fmt.Errorf("re-encode request messages after tool call renaming: %w", err)
	}
	request["messages"] = encoded
	return converted, nil
}

// renameResponseToolCalls applies the renames to the calls the model made in
// this turn, which live in the response rather than the history.
func renameResponseToolCalls(response map[string]json.RawMessage, renames map[string]string) (int, error) {
	if len(renames) == 0 || response == nil {
		return 0, nil
	}
	renamed, count, err := renameContentToolCalls(response["content"], renames)
	if err != nil || count == 0 {
		return 0, err
	}
	response["content"] = renamed
	return count, nil
}

// renameContentToolCalls rewrites the tool calls in one content array onto their
// Claude Code targets, mapping each call's arguments onto the target schema.
func renameContentToolCalls(
	content json.RawMessage,
	renames map[string]string,
) (json.RawMessage, int, error) {
	if !isJSONArray(content) {
		return nil, 0, nil
	}
	var blocks []map[string]json.RawMessage
	if err := json.Unmarshal(content, &blocks); err != nil {
		return nil, 0, fmt.Errorf("decode content blocks for tool call renaming: %w", err)
	}
	converted := 0
	for _, block := range blocks {
		if !isToolCallBlock(rawString(block["type"])) {
			continue
		}
		target, ok := renames[rawString(block["name"])]
		if !ok {
			continue
		}
		block["name"] = mustJSON(target)
		input, err := convertForeignToolInput(target, block["input"])
		if err != nil {
			return nil, 0, err
		}
		if input != nil {
			block["input"] = input
		}
		converted++
	}
	if converted == 0 {
		return nil, 0, nil
	}
	encoded, err := json.Marshal(blocks)
	if err != nil {
		return nil, 0, fmt.Errorf("re-encode content blocks after tool call renaming: %w", err)
	}
	return encoded, converted, nil
}

// convertForeignToolInput maps a converted call's arguments onto the Claude Code
// schema of its target tool.
func convertForeignToolInput(target string, raw json.RawMessage) (json.RawMessage, error) {
	renames, ok := foreignArgumentTargets[target]
	if !ok || len(raw) == 0 {
		return nil, nil
	}
	var input map[string]json.RawMessage
	if err := json.Unmarshal(raw, &input); err != nil {
		// A freeform custom tool carries a bare string; the projection that
		// wraps it into an object owns that shape and runs elsewhere.
		return nil, nil //nolint:nilerr // not an object: nothing to rename.
	}
	if target == "Bash" {
		convertBashWorkdir(input)
	}
	droppable := foreignArgumentDroppable[target]
	mapped := make(map[string]json.RawMessage, len(input))
	for key, value := range input {
		if renamed, ok := renames[key]; ok {
			mapped[renamed] = value
			continue
		}
		if !droppable[key] {
			return nil, fmt.Errorf("converted %s call carries argument %q with no Claude Code field", target, key)
		}
	}
	encoded, err := json.Marshal(mapped)
	if err != nil {
		return nil, fmt.Errorf("re-encode converted tool input for %q: %w", target, err)
	}
	return encoded, nil
}

// convertBashWorkdir folds a working directory into the command itself, which is
// how Claude Code expresses it: the Bash schema has no working-directory field,
// so dropping the argument would silently move the command somewhere else,
// while prefixing preserves exactly what ran.
func convertBashWorkdir(input map[string]json.RawMessage) {
	workdir := rawString(input["workdir"])
	command := rawString(input["command"])
	if workdir == "" || command == "" {
		return
	}
	input["command"] = mustJSON("cd " + shellQuote(workdir) + " && " + command)
}

// shellQuote renders a path as a single POSIX shell word.
func shellQuote(value string) string {
	if !strings.ContainsAny(value, " \t\n'\"\\$`*?[]();&|<>#~") {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}
