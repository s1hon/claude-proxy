package convert

import (
	"fmt"
	"strings"

	"github.com/s1hon/claude-proxy/internal/openai"
)

// CompactOptions tunes the truncation caps used by MessagesCompact. Zero
// values fall back to the same defaults as the upstream bridge.
type CompactOptions struct {
	AssistantCap  int // per-message cap for assistant text (default 1500)
	RecentToolCap int // cap for tool results in the last N user turns (default 2000)
	OldToolCap    int // cap for older tool results (default 500)
	RecentTurns   int // how many trailing user turns count as "recent" (default 10)
}

func (o CompactOptions) withDefaults() CompactOptions {
	if o.AssistantCap == 0 {
		o.AssistantCap = 1500
	}
	if o.RecentToolCap == 0 {
		o.RecentToolCap = 2000
	}
	if o.OldToolCap == 0 {
		o.OldToolCap = 500
	}
	if o.RecentTurns == 0 {
		o.RecentTurns = 10
	}
	return o
}

// MessagesCompact is a size-constrained variant of Messages used when the
// upstream has compacted the conversation and we need to rebuild a CLI session
// cheaply. It truncates assistant text and tool results, keeping more detail
// for recent turns.
func MessagesCompact(msgs []openai.Message, opts CompactOptions) Converted {
	opts = opts.withDefaults()

	var userTurns int
	for _, m := range msgs {
		if m.Role == "user" {
			userTurns++
		}
	}
	recentCutoff := userTurns - opts.RecentTurns
	if recentCutoff < 0 {
		recentCutoff = 0
	}

	var systemParts, convParts []string
	currentUserTurn := 0

	for _, m := range msgs {
		content := ExtractContent(m.Content)
		switch m.Role {
		case "system", "developer":
			if content != "" {
				systemParts = append(systemParts, content)
			}
		case "user":
			currentUserTurn++
			if content != "" {
				convParts = append(convParts, "User: "+content)
			}
		case "assistant":
			var parts []string
			if content != "" {
				if len(content) > opts.AssistantCap {
					parts = append(parts, content[:opts.AssistantCap]+"\n[... truncated]")
				} else {
					parts = append(parts, content)
				}
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
			if content == "" {
				continue
			}
			cap := opts.OldToolCap
			if currentUserTurn >= recentCutoff {
				cap = opts.RecentToolCap
			}
			if len(content) > cap {
				content = content[:cap] + "\n[... truncated]"
			}
			convParts = append(convParts, fmt.Sprintf(
				"<tool_result name=%q tool_call_id=%q>\n%s\n</tool_result>",
				m.Name, m.ToolCallID, content))
		}
	}

	return Converted{
		SystemPrompt: strings.Join(systemParts, "\n\n"),
		PromptText:   strings.Join(convParts, "\n\n"),
	}
}
