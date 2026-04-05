package tools

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"strings"

	"github.com/s1hon/claude-proxy/internal/openai"
)

var (
	toolCallRe        = regexp.MustCompile(`(?s)<tool_call>\s*(\{.*?\})\s*</tool_call>`)
	strayToolCallRe   = regexp.MustCompile(`(?s)<tool_call>.*?</tool_call>`)
	strayToolResultRe = regexp.MustCompile(`(?s)<tool_result[^>]*>.*?</tool_result>`)
	strayPrevRe       = regexp.MustCompile(`(?s)<previous_response>.*?</previous_response>`)
)

// ParseToolCalls extracts every <tool_call>...</tool_call> JSON block from
// text, returning them as OpenAI ToolCall structs with freshly generated IDs.
// Invalid JSON blocks are skipped silently (matching the upstream behaviour).
func ParseToolCalls(text string) []openai.ToolCall {
	matches := toolCallRe.FindAllStringSubmatch(text, -1)
	if len(matches) == 0 {
		return nil
	}
	out := make([]openai.ToolCall, 0, len(matches))
	for _, m := range matches {
		var parsed struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal([]byte(m[1]), &parsed); err != nil {
			continue
		}
		if parsed.Name == "" {
			continue
		}
		args := string(parsed.Arguments)
		if args == "" {
			args = "{}"
		}
		out = append(out, openai.ToolCall{
			ID:   newCallID(),
			Type: "function",
			Function: openai.ToolCallFunction{
				Name:      parsed.Name,
				Arguments: args,
			},
		})
	}
	return out
}

// CleanText strips internal XML tags (tool_call/tool_result/previous_response)
// so they do not leak to end users. Whitespace is collapsed around the removed
// blocks.
func CleanText(text string) string {
	text = strayToolCallRe.ReplaceAllString(text, "")
	text = strayToolResultRe.ReplaceAllString(text, "")
	text = strayPrevRe.ReplaceAllString(text, "")
	// Collapse runs of 3+ newlines
	text = regexp.MustCompile(`\n{3,}`).ReplaceAllString(text, "\n\n")
	return strings.TrimSpace(text)
}

func newCallID() string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return "call_" + hex.EncodeToString(b[:])
}
