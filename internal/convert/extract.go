package convert

import (
	"fmt"
	"strings"

	"github.com/s1hon/claude-proxy/internal/openai"
)

const defaultToolResultCap = 15000

// ExtractNewMessages is used in --resume mode during a tool loop. It finds the
// last assistant message with tool_calls and returns everything after it as
// formatted text. Returns empty string if no active tool loop is found.
func ExtractNewMessages(msgs []openai.Message, toolResultCap int) string {
	if toolResultCap <= 0 {
		toolResultCap = defaultToolResultCap
	}

	lastIdx := -1
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == "assistant" && len(msgs[i].ToolCalls) > 0 {
			lastIdx = i
			break
		}
	}
	if lastIdx == -1 {
		return ""
	}
	// Tool loop is over if a later assistant message exists.
	for i := lastIdx + 1; i < len(msgs); i++ {
		if msgs[i].Role == "assistant" {
			return ""
		}
	}

	return formatTail(msgs[lastIdx+1:], toolResultCap)
}

// ExtractNewUserMessages is used in --resume mode for simple continuation.
// It returns everything after the last assistant message.
func ExtractNewUserMessages(msgs []openai.Message, toolResultCap int) string {
	if toolResultCap <= 0 {
		toolResultCap = defaultToolResultCap
	}

	lastIdx := -1
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == "assistant" {
			lastIdx = i
			break
		}
	}
	if lastIdx == -1 {
		return ""
	}
	tail := msgs[lastIdx+1:]
	if len(tail) == 0 {
		return ""
	}
	return formatTail(tail, toolResultCap)
}

func formatTail(tail []openai.Message, cap int) string {
	var parts []string
	for _, m := range tail {
		switch m.Role {
		case "user":
			if c := ExtractContent(m.Content); c != "" {
				parts = append(parts, c)
			}
		case "tool":
			c := ExtractContent(m.Content)
			if c == "" {
				continue
			}
			if len(c) > cap {
				c = c[:cap] + "\n[... truncated]"
			}
			parts = append(parts, fmt.Sprintf(
				"<tool_result name=%q tool_call_id=%q>\n%s\n</tool_result>",
				m.Name, m.ToolCallID, c))
		}
	}
	return strings.Join(parts, "\n\n")
}
