package tools

import (
	"strings"
	"testing"

	"github.com/s1hon/claude-proxy/internal/openai"
)

func makeTool(name, desc string) openai.Tool {
	return openai.Tool{
		Type: "function",
		Function: openai.ToolFunction{
			Name:        name,
			Description: desc,
		},
	}
}

// TestBuildInstructions covers empty tools, normal tools, blocked tools, and
// the presence of <tool_call> protocol text.
func TestBuildInstructions(t *testing.T) {
	tests := []struct {
		name           string
		tools          []openai.Tool
		wantEmpty      bool
		wantContains   []string
		wantNotContain []string
	}{
		{
			name:      "nil tools returns empty string",
			tools:     nil,
			wantEmpty: true,
		},
		{
			name:      "empty slice returns empty string",
			tools:     []openai.Tool{},
			wantEmpty: true,
		},
		{
			name:  "single tool appears in output",
			tools: []openai.Tool{makeTool("web_search", "Search the web")},
			wantContains: []string{
				"<tool_call>",
				"web_search",
				"Search the web",
			},
		},
		{
			name: "multiple tools all appear",
			tools: []openai.Tool{
				makeTool("tool_a", "Does A"),
				makeTool("tool_b", "Does B"),
			},
			wantContains: []string{"tool_a", "Does A", "tool_b", "Does B"},
		},
		{
			name: "sessions_send is filtered out",
			tools: []openai.Tool{
				makeTool("sessions_send", "internal"),
				makeTool("public_tool", "visible"),
			},
			wantContains:   []string{"public_tool"},
			wantNotContain: []string{"sessions_send"},
		},
		{
			name: "sessions_spawn is filtered out",
			tools: []openai.Tool{
				makeTool("sessions_spawn", "internal"),
				makeTool("another_tool", "visible"),
			},
			wantContains:   []string{"another_tool"},
			wantNotContain: []string{"sessions_spawn"},
		},
		{
			name: "gateway is filtered out",
			tools: []openai.Tool{
				makeTool("gateway", "internal"),
				makeTool("safe_tool", "visible"),
			},
			wantContains:   []string{"safe_tool"},
			wantNotContain: []string{"gateway"},
		},
		{
			name: "all tools blocked still returns protocol header",
			tools: []openai.Tool{
				makeTool("sessions_send", "internal"),
				makeTool("sessions_spawn", "internal"),
				makeTool("gateway", "internal"),
			},
			// Non-empty because the header is written before iterating tools.
			wantEmpty:      false,
			wantContains:   []string{"<tool_call>"},
			wantNotContain: []string{"sessions_send", "sessions_spawn", "gateway"},
		},
		{
			name: "tool with empty name is skipped",
			tools: []openai.Tool{
				makeTool("", "no name"),
				makeTool("real_tool", "has name"),
			},
			wantContains: []string{"real_tool"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := BuildInstructions(tc.tools)
			if tc.wantEmpty && got != "" {
				t.Errorf("expected empty string, got %q", got)
			}
			if !tc.wantEmpty && got == "" {
				t.Errorf("expected non-empty string")
			}
			for _, want := range tc.wantContains {
				if !strings.Contains(got, want) {
					t.Errorf("output does not contain %q\nOutput: %s", want, got)
				}
			}
			for _, notWant := range tc.wantNotContain {
				if strings.Contains(got, notWant) {
					t.Errorf("output should not contain %q\nOutput: %s", notWant, got)
				}
			}
		})
	}
}
