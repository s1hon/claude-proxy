package convert

import (
	"strings"
	"testing"

	"github.com/s1hon/claude-proxy/internal/openai"
)

func toolMsg(name, callID, content string) openai.Message {
	return openai.Message{
		Role:       "tool",
		Name:       name,
		ToolCallID: callID,
		Content:    rawString(content),
	}
}

func assistantWithToolCalls(calls ...openai.ToolCall) openai.Message {
	return openai.Message{
		Role:      "assistant",
		ToolCalls: calls,
	}
}

func userMsg(content string) openai.Message {
	return openai.Message{
		Role:    "user",
		Content: rawString(content),
	}
}

func assistantMsg(content string) openai.Message {
	return openai.Message{
		Role:    "assistant",
		Content: rawString(content),
	}
}

func dummyToolCall(id, name string) openai.ToolCall {
	return openai.ToolCall{
		ID:   id,
		Type: "function",
		Function: openai.ToolCallFunction{
			Name:      name,
			Arguments: `{}`,
		},
	}
}

// TestExtractNewMessages covers the tool-loop detection path.
func TestExtractNewMessages(t *testing.T) {
	tests := []struct {
		name         string
		msgs         []openai.Message
		cap          int
		wantEmpty    bool
		wantContains []string
	}{
		{
			name:      "no assistant message returns empty",
			msgs:      []openai.Message{userMsg("hi")},
			cap:       0,
			wantEmpty: true,
		},
		{
			name: "assistant without tool_calls returns empty",
			msgs: []openai.Message{
				userMsg("hi"),
				assistantMsg("hello"),
			},
			cap:       0,
			wantEmpty: true,
		},
		{
			name: "active tool loop returns tool results",
			msgs: []openai.Message{
				userMsg("do something"),
				assistantWithToolCalls(dummyToolCall("c1", "search")),
				toolMsg("search", "c1", "search result"),
			},
			cap:          0,
			wantEmpty:    false,
			wantContains: []string{`<tool_result name="search" tool_call_id="c1">`, "search result"},
		},
		{
			name: "tool loop closed by later assistant returns empty",
			msgs: []openai.Message{
				userMsg("do something"),
				assistantWithToolCalls(dummyToolCall("c1", "search")),
				toolMsg("search", "c1", "result"),
				assistantMsg("final answer"),
			},
			cap:       0,
			wantEmpty: true,
		},
		{
			name: "user message in tail is included",
			msgs: []openai.Message{
				assistantWithToolCalls(dummyToolCall("c1", "tool_a")),
				userMsg("follow up"),
				toolMsg("tool_a", "c1", "data"),
			},
			cap:          0,
			wantEmpty:    false,
			wantContains: []string{"follow up", "data"},
		},
		{
			name: "tool result truncated when over cap",
			msgs: []openai.Message{
				assistantWithToolCalls(dummyToolCall("c1", "big_tool")),
				toolMsg("big_tool", "c1", strings.Repeat("x", 100)),
			},
			cap:          10,
			wantEmpty:    false,
			wantContains: []string{"[... truncated]"},
		},
		{
			name: "tool result not truncated when under cap",
			msgs: []openai.Message{
				assistantWithToolCalls(dummyToolCall("c1", "small_tool")),
				toolMsg("small_tool", "c1", "short result"),
			},
			cap:          10000,
			wantEmpty:    false,
			wantContains: []string{"short result"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ExtractNewMessages(tc.msgs, tc.cap)
			if tc.wantEmpty && got != "" {
				t.Errorf("expected empty, got %q", got)
			}
			if !tc.wantEmpty && got == "" {
				t.Errorf("expected non-empty result")
			}
			for _, want := range tc.wantContains {
				if !strings.Contains(got, want) {
					t.Errorf("result %q does not contain %q", got, want)
				}
			}
		})
	}
}

// TestExtractNewUserMessages covers simple continuation path.
func TestExtractNewUserMessages(t *testing.T) {
	tests := []struct {
		name         string
		msgs         []openai.Message
		cap          int
		wantEmpty    bool
		wantContains []string
	}{
		{
			name:      "no assistant message returns empty",
			msgs:      []openai.Message{userMsg("hello")},
			cap:       0,
			wantEmpty: true,
		},
		{
			name: "nothing after last assistant returns empty",
			msgs: []openai.Message{
				userMsg("hi"),
				assistantMsg("hello"),
			},
			cap:       0,
			wantEmpty: true,
		},
		{
			name: "user message after assistant is returned",
			msgs: []openai.Message{
				assistantMsg("previous answer"),
				userMsg("new question"),
			},
			cap:          0,
			wantEmpty:    false,
			wantContains: []string{"new question"},
		},
		{
			name: "tool result after assistant is included",
			msgs: []openai.Message{
				assistantWithToolCalls(dummyToolCall("c1", "tool_a")),
				toolMsg("tool_a", "c1", "tool output"),
			},
			cap:          0,
			wantEmpty:    false,
			wantContains: []string{"tool output"},
		},
		{
			name: "tool result over cap is truncated",
			msgs: []openai.Message{
				assistantMsg("answer"),
				toolMsg("big_tool", "c1", strings.Repeat("y", 200)),
			},
			cap:          20,
			wantEmpty:    false,
			wantContains: []string{"[... truncated]"},
		},
		{
			name: "picks last assistant as boundary",
			msgs: []openai.Message{
				userMsg("first"),
				assistantMsg("first reply"),
				userMsg("second"),
				assistantMsg("second reply"),
				userMsg("third"),
			},
			cap:          0,
			wantEmpty:    false,
			wantContains: []string{"third"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ExtractNewUserMessages(tc.msgs, tc.cap)
			if tc.wantEmpty && got != "" {
				t.Errorf("expected empty, got %q", got)
			}
			if !tc.wantEmpty && got == "" {
				t.Errorf("expected non-empty result")
			}
			for _, want := range tc.wantContains {
				if !strings.Contains(got, want) {
					t.Errorf("result %q does not contain %q", got, want)
				}
			}
		})
	}
}
