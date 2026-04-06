// Package claude manages the Claude Code CLI subprocess lifecycle.
package claude

import "strings"

// ResolveModel maps incoming OpenAI-style model IDs to the CLI alias configured
// via env. Unknown IDs are passed through untouched so callers can use raw CLI
// names directly.
func ResolveModel(modelID, opus, sonnet, haiku string) string {
	switch modelID {
	case "claude-opus-latest", "claude-opus", "opus":
		if opus != "" {
			return opus
		}
		return "opus"
	case "claude-sonnet-latest", "claude-sonnet", "sonnet":
		if sonnet != "" {
			return sonnet
		}
		return "sonnet"
	case "claude-haiku-latest", "claude-haiku", "haiku":
		if haiku != "" {
			return haiku
		}
		return "haiku"
	default:
		return modelID
	}
}

// KnownModel reports whether modelID is a recognised alias that ResolveModel
// can map to a CLI model. Unknown IDs (e.g. "google/gemini-3-flash-preview")
// should be rejected at the HTTP layer.
func KnownModel(modelID string) bool {
	switch modelID {
	case "claude-opus-latest", "claude-opus", "opus",
		"claude-sonnet-latest", "claude-sonnet", "sonnet",
		"claude-haiku-latest", "claude-haiku", "haiku":
		return true
	default:
		return false
	}
}

// ContextWindow returns 1,000,000 for 1m-context variants, 200,000 otherwise.
func ContextWindow(resolvedModel string) int {
	if strings.Contains(resolvedModel, "[1m]") {
		return 1_000_000
	}
	return 200_000
}

// MapEffort converts OpenAI reasoning_effort levels to Claude CLI --effort.
// Empty string means thinking is disabled.
func MapEffort(reasoningEffort string) string {
	switch reasoningEffort {
	case "":
		return ""
	case "minimal", "low":
		return "low"
	case "medium":
		return "medium"
	case "high", "xhigh":
		return "high"
	default:
		return ""
	}
}
