package convert

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/s1hon/claude-proxy/internal/openai"
)

func strContent(s string) json.RawMessage {
	b, _ := json.Marshal(s)
	return b
}

func TestMessagesCompact_Defaults(t *testing.T) {
	opts := CompactOptions{}.withDefaults()
	if opts.AssistantCap != 1500 || opts.RecentToolCap != 2000 ||
		opts.OldToolCap != 500 || opts.RecentTurns != 10 {
		t.Fatalf("unexpected defaults: %+v", opts)
	}
}

func TestMessagesCompact_AssistantTruncation(t *testing.T) {
	long := strings.Repeat("a", 2000)
	msgs := []openai.Message{
		{Role: "user", Content: strContent("hi")},
		{Role: "assistant", Content: strContent(long)},
	}

	out := MessagesCompact(msgs, CompactOptions{AssistantCap: 100})
	if !strings.Contains(out.PromptText, "[... truncated]") {
		t.Errorf("expected truncation marker, got:\n%s", out.PromptText)
	}
	if strings.Count(out.PromptText, "a") > 150 { // 100 + some overhead
		t.Errorf("assistant text not truncated to cap")
	}
}

func TestMessagesCompact_RecentVsOldToolCap(t *testing.T) {
	// Build 12 user turns. Tool result after turn 1 is "old", after turn 12 is "recent".
	bigResult := strings.Repeat("x", 3000)
	var msgs []openai.Message
	msgs = append(msgs, openai.Message{Role: "user", Content: strContent("u1")})
	msgs = append(msgs, openai.Message{
		Role: "tool", Name: "search", ToolCallID: "c1",
		Content: strContent(bigResult),
	})
	for i := 2; i <= 12; i++ {
		msgs = append(msgs, openai.Message{Role: "user", Content: strContent("u")})
	}
	msgs = append(msgs, openai.Message{
		Role: "tool", Name: "search", ToolCallID: "c12",
		Content: strContent(bigResult),
	})

	out := MessagesCompact(msgs, CompactOptions{})

	// Old tool result should be capped at 500; recent at 2000. The recent one
	// should therefore contain roughly 4x as many 'x' characters as the old.
	// Find the two tool_result blocks and compare x counts.
	blocks := strings.Split(out.PromptText, "<tool_result")
	if len(blocks) < 3 {
		t.Fatalf("expected 2 tool_result blocks, got %d", len(blocks)-1)
	}
	// blocks[1] is the first tool_result (old), blocks[2] is the second (recent)
	oldX := strings.Count(blocks[1], "x")
	recentX := strings.Count(blocks[2], "x")
	if oldX > 600 { // cap 500 + some slack for marker
		t.Errorf("old tool result not capped at 500: got %d x", oldX)
	}
	if recentX < 1500 || recentX > 2100 {
		t.Errorf("recent tool result not around 2000: got %d x", recentX)
	}
}

func TestMessagesCompact_SystemAndConversation(t *testing.T) {
	msgs := []openai.Message{
		{Role: "developer", Content: strContent("**Name:** Bot")},
		{Role: "system", Content: strContent("be nice")},
		{Role: "user", Content: strContent("q1")},
		{Role: "assistant", Content: strContent("a1")},
		{Role: "user", Content: strContent("q2")},
	}
	out := MessagesCompact(msgs, CompactOptions{})
	if !strings.Contains(out.SystemPrompt, "Name") || !strings.Contains(out.SystemPrompt, "be nice") {
		t.Errorf("system prompt missing parts: %q", out.SystemPrompt)
	}
	if !strings.Contains(out.PromptText, "User: q1") || !strings.Contains(out.PromptText, "User: q2") {
		t.Errorf("conversation missing user turns: %q", out.PromptText)
	}
	if !strings.Contains(out.PromptText, "<previous_response>") {
		t.Errorf("assistant turn not wrapped: %q", out.PromptText)
	}
}

func TestMessagesCompact_AssistantToolCalls(t *testing.T) {
	msgs := []openai.Message{
		{Role: "user", Content: strContent("hi")},
		{
			Role:    "assistant",
			Content: strContent("sure"),
			ToolCalls: []openai.ToolCall{{
				ID:   "c1",
				Type: "function",
				Function: openai.ToolCallFunction{
					Name:      "web_search",
					Arguments: `{"q":"bitcoin"}`,
				},
			}},
		},
	}
	out := MessagesCompact(msgs, CompactOptions{})
	if !strings.Contains(out.PromptText, "<tool_call>") ||
		!strings.Contains(out.PromptText, `"name": "web_search"`) {
		t.Errorf("tool_call not rendered: %q", out.PromptText)
	}
}
