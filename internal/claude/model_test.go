package claude

import (
	"testing"
)

// TestResolveModel covers all alias mappings and passthrough behavior.
func TestResolveModel(t *testing.T) {
	tests := []struct {
		name    string
		modelID string
		opus    string
		sonnet  string
		haiku   string
		want    string
	}{
		// Opus aliases with configured env value.
		{name: "claude-opus-latest with opus env", modelID: "claude-opus-latest", opus: "my-opus", want: "my-opus"},
		{name: "claude-opus with opus env", modelID: "claude-opus", opus: "my-opus", want: "my-opus"},
		{name: "opus with opus env", modelID: "opus", opus: "my-opus", want: "my-opus"},

		// Opus aliases with no env value fall back to "opus".
		{name: "claude-opus-latest no env", modelID: "claude-opus-latest", want: "opus"},
		{name: "claude-opus no env", modelID: "claude-opus", want: "opus"},
		{name: "opus no env", modelID: "opus", want: "opus"},

		// Sonnet aliases with configured env value.
		{name: "claude-sonnet-latest with sonnet env", modelID: "claude-sonnet-latest", sonnet: "my-sonnet", want: "my-sonnet"},
		{name: "claude-sonnet with sonnet env", modelID: "claude-sonnet", sonnet: "my-sonnet", want: "my-sonnet"},
		{name: "sonnet with sonnet env", modelID: "sonnet", sonnet: "my-sonnet", want: "my-sonnet"},

		// Sonnet aliases with no env value fall back to "sonnet".
		{name: "claude-sonnet-latest no env", modelID: "claude-sonnet-latest", want: "sonnet"},
		{name: "claude-sonnet no env", modelID: "claude-sonnet", want: "sonnet"},
		{name: "sonnet no env", modelID: "sonnet", want: "sonnet"},

		// Haiku aliases with configured env value.
		{name: "claude-haiku-latest with haiku env", modelID: "claude-haiku-latest", haiku: "my-haiku", want: "my-haiku"},
		{name: "claude-haiku with haiku env", modelID: "claude-haiku", haiku: "my-haiku", want: "my-haiku"},
		{name: "haiku with haiku env", modelID: "haiku", haiku: "my-haiku", want: "my-haiku"},

		// Haiku aliases with no env value fall back to "haiku".
		{name: "claude-haiku-latest no env", modelID: "claude-haiku-latest", want: "haiku"},
		{name: "claude-haiku no env", modelID: "claude-haiku", want: "haiku"},
		{name: "haiku no env", modelID: "haiku", want: "haiku"},

		// Unknown model IDs pass through unchanged.
		{name: "raw CLI model name passthrough", modelID: "claude-3-7-sonnet-20250219", want: "claude-3-7-sonnet-20250219"},
		{name: "empty model passthrough", modelID: "", want: ""},
		{name: "unknown string passthrough", modelID: "gpt-4o", want: "gpt-4o"},

		// Env value for a different tier does not bleed over.
		{name: "sonnet env does not affect opus", modelID: "opus", sonnet: "my-sonnet", want: "opus"},
		{name: "haiku env does not affect sonnet", modelID: "sonnet", haiku: "my-haiku", want: "sonnet"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ResolveModel(tc.modelID, tc.opus, tc.sonnet, tc.haiku)
			if got != tc.want {
				t.Errorf("ResolveModel(%q, %q, %q, %q) = %q, want %q",
					tc.modelID, tc.opus, tc.sonnet, tc.haiku, got, tc.want)
			}
		})
	}
}

// TestContextWindow covers [1m] detection and the default 200k.
func TestContextWindow(t *testing.T) {
	tests := []struct {
		name  string
		model string
		want  int
	}{
		{name: "contains [1m]", model: "sonnet[1m]", want: 1_000_000},
		{name: "contains [1m] with prefix", model: "my-model[1m]-extra", want: 1_000_000},
		{name: "no [1m] returns 200k", model: "sonnet", want: 200_000},
		{name: "empty model returns 200k", model: "", want: 200_000},
		{name: "partial bracket no match", model: "model1m", want: 200_000},
		{name: "claude-opus [1m]", model: "claude-opus-3[1m]", want: 1_000_000},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ContextWindow(tc.model)
			if got != tc.want {
				t.Errorf("ContextWindow(%q) = %d, want %d", tc.model, got, tc.want)
			}
		})
	}
}

// TestMapEffort covers all defined effort levels and the unknown fallback.
func TestMapEffort(t *testing.T) {
	tests := []struct {
		name   string
		effort string
		want   string
	}{
		{name: "empty string returns empty", effort: "", want: ""},
		{name: "minimal maps to low", effort: "minimal", want: "low"},
		{name: "low maps to low", effort: "low", want: "low"},
		{name: "medium maps to medium", effort: "medium", want: "medium"},
		{name: "high maps to high", effort: "high", want: "high"},
		{name: "xhigh maps to high", effort: "xhigh", want: "high"},
		{name: "unknown returns empty", effort: "turbo", want: ""},
		{name: "uppercase unknown returns empty", effort: "HIGH", want: ""},
		{name: "arbitrary string returns empty", effort: "supermax", want: ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := MapEffort(tc.effort)
			if got != tc.want {
				t.Errorf("MapEffort(%q) = %q, want %q", tc.effort, got, tc.want)
			}
		})
	}
}
