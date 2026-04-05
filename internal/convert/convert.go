// Package convert translates OpenAI chat messages into the plain-text format
// that Claude Code CLI accepts on stdin.
package convert

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/s1hon/claude-proxy/internal/openai"
)

// Converted holds the two pieces the Claude CLI needs: a system prompt and the
// conversation text fed to stdin.
type Converted struct {
	SystemPrompt string
	PromptText   string
}

// Messages converts the full message history into system prompt + conversation text.
func Messages(msgs []openai.Message) Converted {
	var systemParts, convParts []string

	for _, m := range msgs {
		content := ExtractContent(m.Content)
		switch m.Role {
		case "system", "developer":
			if content != "" {
				systemParts = append(systemParts, content)
			}
		case "user":
			if content != "" {
				convParts = append(convParts, "User: "+content)
			}
		case "assistant":
			var parts []string
			if content != "" {
				parts = append(parts, content)
			}
			for _, tc := range m.ToolCalls {
				args := tc.Function.Arguments
				if args == "" {
					args = "{}"
				}
				parts = append(parts, fmt.Sprintf(
					"<tool_call>\n{\"name\": %q, \"arguments\": %s}\n</tool_call>",
					tc.Function.Name, args))
			}
			if len(parts) > 0 {
				convParts = append(convParts,
					"<previous_response>\n"+strings.Join(parts, "\n")+"\n</previous_response>")
			}
		case "tool":
			if content != "" {
				convParts = append(convParts, fmt.Sprintf(
					"<tool_result name=%q tool_call_id=%q>\n%s\n</tool_result>",
					m.Name, m.ToolCallID, content))
			}
		}
	}

	return Converted{
		SystemPrompt: strings.Join(systemParts, "\n\n"),
		PromptText:   strings.Join(convParts, "\n\n"),
	}
}

// ExtractContent flattens a message content field (string or array of parts)
// into a single text string, ignoring non-text parts.
func ExtractContent(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	// String case
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	// Array of parts
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &parts); err == nil {
		var out []string
		for _, p := range parts {
			if p.Type == "text" && p.Text != "" {
				out = append(out, p.Text)
			}
		}
		return strings.Join(out, "\n")
	}
	return ""
}
