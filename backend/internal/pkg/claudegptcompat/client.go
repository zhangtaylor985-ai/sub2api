package claudegptcompat

import "strings"

type ClientKind string

const (
	ClientUnknown      ClientKind = "unknown"
	ClientClaudeCLI    ClientKind = "claude-cli"
	ClientClaudeVSCode ClientKind = "claude_vscode"
	ClientCodexVSCode  ClientKind = "codex_exec_vscode"
)

func NormalizeClientKind(kind ClientKind) ClientKind {
	switch kind {
	case ClientClaudeCLI, ClientClaudeVSCode, ClientCodexVSCode:
		return kind
	default:
		return ClientUnknown
	}
}

func DetectClientKind(userAgent, originator string) ClientKind {
	userAgent = strings.ToLower(strings.TrimSpace(userAgent))
	originator = strings.ToLower(strings.TrimSpace(originator))

	switch {
	case strings.Contains(userAgent, "claude-vscode"):
		return ClientClaudeVSCode
	case strings.HasPrefix(userAgent, "claude-cli/"):
		return ClientClaudeCLI
	case strings.Contains(userAgent, "vscode/"), originator == "codex_exec":
		return ClientCodexVSCode
	default:
		return ClientUnknown
	}
}

func ShouldEmitSyntheticWebSearchTag(kind ClientKind) bool {
	return kind == ClientClaudeCLI
}

func ShouldEmitVSCodeWebSearchProgress(kind ClientKind) bool {
	switch kind {
	case ClientClaudeVSCode, ClientCodexVSCode:
		return true
	default:
		return false
	}
}

func ShouldSurfaceReasoningSummaryAsThinking(kind ClientKind) bool {
	switch kind {
	case ClientClaudeCLI, ClientClaudeVSCode, ClientCodexVSCode:
		return false
	default:
		return true
	}
}
