package sessiondelivery

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// The embedded definitions are the source of every converted tool, so they have
// to be present, well formed and free of any user's own content.
func TestEmbeddedClaudeCodeToolDefinitions(t *testing.T) {
	tools, err := loadClaudeCodeTools()
	require.NoError(t, err)

	for _, target := range foreignToolTargets {
		require.Contains(t, tools, target, "no embedded definition for mapping target %q", target)
	}
	for name, raw := range tools {
		require.True(t, isClaudeCodeToolName(name), "embedded tool %q is not a Claude Code tool", name)
		var tool struct {
			Name        string          `json:"name"`
			Description string          `json:"description"`
			InputSchema json.RawMessage `json:"input_schema"`
		}
		require.NoError(t, json.Unmarshal(raw, &tool))
		require.Equal(t, name, tool.Name)
		require.NotEmpty(t, tool.Description)
		require.NotEmpty(t, tool.InputSchema)
		// A definition is copied into records from other sessions, so it must
		// not carry the paths or settings of the session it was lifted from.
		for _, leak := range []string{"/Users/", "/home/", ".claude/", "settings.json"} {
			require.NotContains(t, raw, leak, "embedded tool %q leaks %q", name, leak)
		}
	}
}

// The embedded definitions live in a process-wide map and a converted record
// borrows their bytes, so conversion runs concurrently across captured requests
// must never write through to them. Run under -race this fails if a converted
// definition is ever mutated in place instead of copied on marshal.
func TestConvertForeignClientToolsIsSafeForConcurrentCaptures(t *testing.T) {
	before, err := loadClaudeCodeTools()
	require.NoError(t, err)
	pristine := make(map[string]string, len(before))
	for name, raw := range before {
		pristine[name] = string(raw)
	}

	newForeignRequest := func() (map[string]json.RawMessage, map[string]json.RawMessage) {
		request := map[string]json.RawMessage{
			"tools": mustJSON([]any{
				map[string]any{"name": "shell_command", "description": "Runs a Powershell command (Windows)."},
				map[string]any{"name": "read", "description": "Reads a file."},
			}),
			"messages": mustJSON([]any{
				map[string]any{"role": "user", "content": []any{
					map[string]any{"type": "text", "text": "run it"},
				}},
				map[string]any{"role": "assistant", "content": []any{
					map[string]any{
						"type":  "tool_use",
						"id":    "toolu_01aaaaaaaaaaaaaaaaaaaaaa",
						"name":  "shell_command",
						"input": map[string]any{"command": "ls"},
					},
				}},
			}),
		}
		response := map[string]json.RawMessage{"content": mustJSON([]any{})}
		return request, response
	}

	const workers = 16
	results := make([]string, workers)
	errs := make([]error, workers)
	var wait sync.WaitGroup
	for worker := range workers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			request, response := newForeignRequest()
			if _, err := convertForeignClientTools(request, response); err != nil {
				errs[worker] = err
				return
			}
			results[worker] = string(request["tools"])
		}()
	}
	wait.Wait()

	for worker := range workers {
		require.NoError(t, errs[worker])
		require.Equal(t, results[0], results[worker], "concurrent conversions must agree")
	}
	require.Contains(t, results[0], `"Bash"`)

	after, err := loadClaudeCodeTools()
	require.NoError(t, err)
	require.Len(t, after, len(pristine))
	for name, raw := range after {
		require.Equal(t, pristine[name], string(raw), "embedded definition %q was mutated by conversion", name)
	}
}

func TestConvertForeignClientToolsReplacesDeclarationsWithClaudeCodeDefinitions(t *testing.T) {
	request := map[string]json.RawMessage{
		"tools": mustJSON([]any{
			map[string]any{"name": "shell_command", "description": "Runs a Powershell command (Windows)."},
			map[string]any{"name": "apply_patch", "description": "The `apply_patch` tool can be used to edit files."},
			map[string]any{"name": "update_plan"},
			// Shares TaskUpdate with update_plan; the declaration is emitted once.
			map[string]any{"name": "update_goal"},
			map[string]any{"name": "web_search"},
		}),
	}
	stats, err := convertForeignClientTools(request, nil)
	require.NoError(t, err)
	require.Equal(t, 3, stats.ToolsConverted)
	// update_plan has no counterpart whose schema its arguments fit, and this
	// exchange never called it, so it is dropped rather than carried.
	require.Equal(t, 1, stats.ToolsDropped)

	names, definitions := declaredTools(t, request["tools"])
	require.Equal(t, []string{"Bash", "Edit", "TaskUpdate", "web_search"}, names)
	require.NoError(t, claudeCodeToolSetViolation(request["tools"]))
	// The replacement carries Claude Code's own text, not the foreign client's.
	require.NotContains(t, string(definitions["Bash"]), "Powershell")
	require.Contains(t, string(definitions["Bash"]), "input_schema")
	require.NotContains(t, string(definitions["Edit"]), "apply_patch")
}

// A capability the exchange never used is dropped; nothing the model did is
// touched, and no vendor name survives in a description.
func TestConvertForeignClientToolsDropsUncalledToolsWithoutCounterpart(t *testing.T) {
	request := map[string]json.RawMessage{
		"tools": mustJSON([]any{
			map[string]any{"name": "codex_app", "description": "Tools provided by the Codex app."},
			map[string]any{"name": "canvas"},
			map[string]any{"name": "music_generate"},
			map[string]any{"name": "read"},
		}),
	}
	stats, err := convertForeignClientTools(request, nil)
	require.NoError(t, err)
	require.Equal(t, 3, stats.ToolsDropped)
	require.Equal(t, 1, stats.ToolsConverted)

	names, _ := declaredTools(t, request["tools"])
	require.Equal(t, []string{"Read"}, names)
	require.NotContains(t, strings.ToLower(string(request["tools"])), "codex")
}

// A namespaced tool is already MCP-shaped, so it only needs the prefix. This is
// the treatment that keeps the namespaced Codex projection deliverable.
func TestConvertForeignClientToolsNamespacesExternalTools(t *testing.T) {
	request := map[string]json.RawMessage{
		"tools": mustJSON([]any{
			map[string]any{"name": "collaboration__send_message", "input_schema": map[string]any{"type": "object"}},
		}),
		"messages": mustJSON([]any{
			map[string]any{"role": "assistant", "content": []any{
				map[string]any{"type": "tool_use", "id": "toolu_1", "name": "collaboration__send_message", "input": map[string]any{}},
			}},
		}),
	}
	stats, err := convertForeignClientTools(request, nil)
	require.NoError(t, err)
	require.Equal(t, 1, stats.ToolsConverted)
	require.Equal(t, 1, stats.CallsConverted)

	names, definitions := declaredTools(t, request["tools"])
	require.Equal(t, []string{"mcp__collaboration__send_message"}, names)
	require.Contains(t, string(definitions["mcp__collaboration__send_message"]), "input_schema")
	require.NoError(t, claudeCodeToolSetViolation(request["tools"]))
	require.Equal(t, []string{"mcp__collaboration__send_message"}, historyToolCallNames(t, request))
}

// The two calls this corpus actually contains: read in the history and exec in
// the response, both with arguments the target schema names differently.
func TestConvertForeignClientToolsMapsCallArgumentsOntoTargetSchema(t *testing.T) {
	request := map[string]json.RawMessage{
		"tools": mustJSON([]any{
			map[string]any{"name": "read"},
			map[string]any{"name": "exec"},
		}),
		"messages": mustJSON([]any{
			map[string]any{"role": "assistant", "content": []any{
				map[string]any{"type": "tool_use", "id": "toolu_1", "name": "read", "input": map[string]any{
					"path": "/tmp/SKILL.md", "limit": 20,
				}},
			}},
		}),
	}
	response := map[string]json.RawMessage{
		"content": mustJSON([]any{
			map[string]any{"type": "tool_use", "id": "toolu_2", "name": "exec", "input": map[string]any{
				"command": "find . -name '*.go'", "workdir": "/tmp/work space", "yieldMs": 10000,
			}},
		}),
	}
	stats, err := convertForeignClientTools(request, response)
	require.NoError(t, err)
	require.Equal(t, 2, stats.CallsConverted)

	require.Equal(t, []string{"Read"}, historyToolCallNames(t, request))
	readInput := historyToolCallInputs(t, request)["Read"]
	require.JSONEq(t, `{"file_path":"/tmp/SKILL.md","limit":20}`, string(readInput))

	var responseBlocks []map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(response["content"], &responseBlocks))
	require.Equal(t, "Bash", rawString(responseBlocks[0]["name"]))
	// Bash has no working-directory field, so the directory is folded into the
	// command rather than dropped, which is how Claude Code expresses it.
	require.JSONEq(
		t,
		`{"command":"cd '/tmp/work space' \u0026\u0026 find . -name '*.go'","timeout":10000}`,
		string(responseBlocks[0]["input"]),
	)
}

// Dropping a declaration the model actually called would leave the call
// pointing at nothing, so a called tool with no counterpart is carried as an MCP
// tool instead: the call and its arguments survive untouched, and only the
// vendor naming in the description is rewritten.
func TestConvertForeignClientToolsCarriesCalledToolWithoutCounterpartAsMCP(t *testing.T) {
	request := map[string]json.RawMessage{
		"tools": mustJSON([]any{map[string]any{
			"name":         "browser",
			"description":  "Control the browser via OpenClaw's browser control server.",
			"input_schema": map[string]any{"type": "object"},
		}}),
		"messages": mustJSON([]any{
			map[string]any{"role": "assistant", "content": []any{
				map[string]any{
					"type": "tool_use", "id": "toolu_01aaaaaaaaaaaaaaaaaaaaaa", "name": "browser",
					"input": map[string]any{"action": "open", "target": "https://example.com"},
				},
			}},
		}),
	}
	stats, err := convertForeignClientTools(request, nil)
	require.NoError(t, err)
	require.Equal(t, 1, stats.ToolsConverted)
	require.Zero(t, stats.ToolsDropped)

	names, definitions := declaredTools(t, request["tools"])
	require.Equal(t, []string{"mcp__workspace__browser"}, names)
	require.NoError(t, claudeCodeToolSetViolation(request["tools"]))
	require.NotContains(t, string(definitions["mcp__workspace__browser"]), "OpenClaw")
	require.Contains(t, string(definitions["mcp__workspace__browser"]), "the browser control server")

	// The call is renamed onto it with its arguments intact, because an MCP tool
	// keeps its own schema.
	var messages []map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(request["messages"], &messages))
	var blocks []map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(messages[0]["content"], &blocks))
	require.Equal(t, "mcp__workspace__browser", rawString(blocks[0]["name"]))
	require.JSONEq(t, `{"action":"open","target":"https://example.com"}`, string(blocks[0]["input"]))
}

// A description that points at a tool the conversion renamed away would send the
// model after something the request no longer declares.
func TestConvertForeignClientToolsRewritesDescriptionToolReferences(t *testing.T) {
	request := map[string]json.RawMessage{
		"tools": mustJSON([]any{
			map[string]any{"name": "exec", "description": "Runs a shell command."},
			map[string]any{
				"name":         "wait",
				"description":  "Waits on a yielded `exec` cell and returns new output.",
				"input_schema": map[string]any{"type": "object"},
			},
		}),
		"messages": mustJSON([]any{
			map[string]any{"role": "assistant", "content": []any{
				map[string]any{
					"type": "tool_use", "id": "toolu_01aaaaaaaaaaaaaaaaaaaaaa", "name": "wait",
					"input": map[string]any{"cell_id": "c1"},
				},
			}},
		}),
	}
	_, err := convertForeignClientTools(request, nil)
	require.NoError(t, err)

	_, definitions := declaredTools(t, request["tools"])
	waited := string(definitions["mcp__workspace__wait"])
	require.NotContains(t, waited, "exec")
	require.Contains(t, waited, "`Bash`")
}

// An argument that changes what the call did may not be dropped silently: the
// converted call would claim the model ran something it did not.
func TestConvertForeignClientToolsFailsOnArgumentWithNoTargetField(t *testing.T) {
	request := map[string]json.RawMessage{
		"tools": mustJSON([]any{map[string]any{"name": "exec"}}),
		"messages": mustJSON([]any{
			map[string]any{"role": "assistant", "content": []any{
				map[string]any{"type": "tool_use", "id": "toolu_1", "name": "exec", "input": map[string]any{
					"command": "make", "env": map[string]any{"CGO_ENABLED": "0"},
				}},
			}},
		}),
	}
	_, err := convertForeignClientTools(request, nil)
	require.ErrorContains(t, err, `argument "env" with no Claude Code field`)

	// A hint that only told the originating client how to run the command is
	// dropped, because the result it produced is unaffected.
	request = map[string]json.RawMessage{
		"tools": mustJSON([]any{map[string]any{"name": "exec_command"}}),
		"messages": mustJSON([]any{
			map[string]any{"role": "assistant", "content": []any{
				map[string]any{"type": "tool_use", "id": "toolu_1", "name": "exec_command", "input": map[string]any{
					"cmd": "ls", "justification": "list files", "login": true, "tty": false,
				}},
			}},
		}),
	}
	_, err = convertForeignClientTools(request, nil)
	require.NoError(t, err)
	require.JSONEq(t, `{"command":"ls"}`, string(historyToolCallInputs(t, request)["Bash"]))
}

// A patch cannot be mechanically turned into Claude Code's file_path/old_string/
// new_string triple, so a called apply_patch has to surface rather than produce
// an Edit call with no arguments.
func TestConvertForeignClientToolsFailsOnCalledFreeformPatch(t *testing.T) {
	request := map[string]json.RawMessage{
		"tools": mustJSON([]any{map[string]any{"name": "apply_patch"}}),
		"messages": mustJSON([]any{
			map[string]any{"role": "assistant", "content": []any{
				map[string]any{"type": "tool_use", "id": "toolu_1", "name": "apply_patch", "input": map[string]any{
					"input": "*** Begin Patch\n*** End Patch",
				}},
			}},
		}),
	}
	_, err := convertForeignClientTools(request, nil)
	require.ErrorContains(t, err, `converted Edit call carries argument "input"`)
}

func TestConvertForeignClientToolsLeavesClaudeCodeRequestsUntouched(t *testing.T) {
	tools := mustJSON([]any{
		map[string]any{"name": "Bash", "description": "runs a command"},
		map[string]any{"name": "mcp__plugin_figma_figma__whoami"},
	})
	request := map[string]json.RawMessage{"tools": tools}
	stats, err := convertForeignClientTools(request, nil)
	require.NoError(t, err)
	require.Zero(t, stats.ToolsConverted)
	require.Zero(t, stats.ToolsDropped)
	require.Equal(t, string(tools), string(request["tools"]))
}

// Re-running the conversion on its own output has to be a no-op, because the
// hourly export and the offline rebuild both apply it to records that may
// already have been converted.
func TestConvertForeignClientToolsIsIdempotent(t *testing.T) {
	build := func() (map[string]json.RawMessage, map[string]json.RawMessage) {
		return map[string]json.RawMessage{
				"tools": mustJSON([]any{
					map[string]any{"name": "exec"},
					map[string]any{"name": "read"},
					map[string]any{"name": "music_generate"},
					map[string]any{"name": "collaboration__send_message"},
				}),
				"messages": mustJSON([]any{
					map[string]any{"role": "assistant", "content": []any{
						map[string]any{"type": "tool_use", "id": "toolu_1", "name": "read", "input": map[string]any{"path": "/tmp/a"}},
					}},
				}),
			}, map[string]json.RawMessage{
				"content": mustJSON([]any{
					map[string]any{"type": "tool_use", "id": "toolu_2", "name": "exec", "input": map[string]any{"command": "ls"}},
				}),
			}
	}

	request, response := build()
	first, err := convertForeignClientTools(request, response)
	require.NoError(t, err)
	require.NotZero(t, first.ToolsConverted)
	firstTools, firstMessages, firstContent := request["tools"], request["messages"], response["content"]

	second, err := convertForeignClientTools(request, response)
	require.NoError(t, err)
	require.Zero(t, second.ToolsConverted)
	require.Zero(t, second.ToolsDropped)
	require.Zero(t, second.CallsConverted)
	require.Equal(t, string(firstTools), string(request["tools"]))
	require.Equal(t, string(firstMessages), string(request["messages"]))
	require.Equal(t, string(firstContent), string(response["content"]))
}

// Conversion reaches the declarations and the calls but not the system prompt,
// so a prompt still naming the removed tools leaves the record instructing the
// model to call a surface it no longer declares. The caller holds such a record
// back, so the count has to be reported.
func TestConvertForeignClientToolsReportsSystemPromptNamingRemovedTools(t *testing.T) {
	build := func(system any) map[string]json.RawMessage {
		return map[string]json.RawMessage{
			"system": mustJSON(system),
			"tools": mustJSON([]any{
				map[string]any{"name": "read"},
				map[string]any{"name": "music_generate"},
				map[string]any{"name": "sessions_spawn"},
			}),
		}
	}

	request := build([]any{map[string]any{"type": "text", "text": "" +
		"You are a personal assistant running inside OpenClaw. Available tools: " +
		"- read: Read file contents - music_generate: make audio - sessions_spawn: spawn a child session"}})
	stats, err := convertForeignClientTools(request, nil)
	require.NoError(t, err)
	require.Equal(t, 2, stats.SystemPromptTools)

	// A prompt that never referenced the removed tools leaves a consistent
	// record, so nothing is held back.
	request = build([]any{map[string]any{"type": "text", "text": "You are a helpful assistant."}})
	stats, err = convertForeignClientTools(request, nil)
	require.NoError(t, err)
	require.Zero(t, stats.SystemPromptTools)

	// A single-word name is an ordinary English word, so prose mentioning it is
	// not treated as a tool reference.
	request = build([]any{map[string]any{"type": "text", "text": "Please read the file before editing."}})
	stats, err = convertForeignClientTools(request, nil)
	require.NoError(t, err)
	require.Zero(t, stats.SystemPromptTools)
}

func TestShellQuoteOnlyQuotesWhenNeeded(t *testing.T) {
	require.Equal(t, "/tmp/plain", shellQuote("/tmp/plain"))
	require.Equal(t, "'/tmp/with space'", shellQuote("/tmp/with space"))
	require.Equal(t, `'/tmp/it'\''s'`, shellQuote("/tmp/it's"))
}

func declaredTools(t *testing.T, raw json.RawMessage) ([]string, map[string]json.RawMessage) {
	t.Helper()
	var tools []map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &tools))
	names := make([]string, 0, len(tools))
	definitions := make(map[string]json.RawMessage, len(tools))
	for _, tool := range tools {
		name := rawString(tool["name"])
		names = append(names, name)
		definitions[name] = mustJSON(tool)
	}
	return names, definitions
}

func historyToolCallNames(t *testing.T, request map[string]json.RawMessage) []string {
	t.Helper()
	names := make([]string, 0)
	for name := range historyToolCallInputs(t, request) {
		names = append(names, name)
	}
	return names
}

func historyToolCallInputs(t *testing.T, request map[string]json.RawMessage) map[string]json.RawMessage {
	t.Helper()
	var messages []map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(request["messages"], &messages))
	inputs := make(map[string]json.RawMessage)
	for _, message := range messages {
		if !isJSONArray(message["content"]) {
			continue
		}
		var blocks []map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(message["content"], &blocks))
		for _, block := range blocks {
			if isToolCallBlock(rawString(block["type"])) {
				inputs[rawString(block["name"])] = block["input"]
			}
		}
	}
	return inputs
}
