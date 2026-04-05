package convert

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/s1hon/claude-proxy/internal/openai"
)

func rawString(s string) json.RawMessage {
	b, _ := json.Marshal(s)
	return json.RawMessage(b)
}

func rawParts(parts []struct {
	Type string `json:"type"`
	Text string `json:"text"`
}) json.RawMessage {
	b, _ := json.Marshal(parts)
	return json.RawMessage(b)
}

// TestMessages covers all role types and content shapes.
func TestMessages(t *testing.T) {
	type partSpec struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}

	tests := []struct {
		name           string
		msgs           []openai.Message
		wantSystem     string
		wantContains   []string
		wantNotContain []string
	}{
		{
			name: "system role string content",
			msgs: []openai.Message{
				{Role: "system", Content: rawString("Be helpful.")},
			},
			wantSystem:   "Be helpful.",
			wantContains: []string{},
		},
		{
			name: "developer role string content",
			msgs: []openai.Message{
				{Role: "developer", Content: rawString("Dev instructions.")},
			},
			wantSystem:   "Dev instructions.",
			wantContains: []string{},
		},
		{
			name: "system and developer concatenated",
			msgs: []openai.Message{
				{Role: "system", Content: rawString("Part A.")},
				{Role: "developer", Content: rawString("Part B.")},
			},
			wantSystem:   "Part A.\n\nPart B.",
			wantContains: []string{},
		},
		{
			name: "user role string content",
			msgs: []openai.Message{
				{Role: "user", Content: rawString("Hello there.")},
			},
			wantContains: []string{"User: Hello there."},
		},
		{
			name: "user role array content parts",
			msgs: []openai.Message{
				{Role: "user", Content: func() json.RawMessage {
					parts := []partSpec{{Type: "text", Text: "Hello from parts."}}
					b, _ := json.Marshal(parts)
					return b
				}()},
			},
			wantContains: []string{"User: Hello from parts."},
		},
		{
			name: "assistant role plain content",
			msgs: []openai.Message{
				{Role: "assistant", Content: rawString("I can help.")},
			},
			wantContains: []string{"<previous_response>", "I can help.", "</previous_response>"},
		},
		{
			name: "assistant role with tool_calls",
			msgs: []openai.Message{
				{
					Role:    "assistant",
					Content: rawString(""),
					ToolCalls: []openai.ToolCall{
						{
							ID:   "call_abc",
							Type: "function",
							Function: openai.ToolCallFunction{
								Name:      "my_tool",
								Arguments: `{"key":"val"}`,
							},
						},
					},
				},
			},
			wantContains: []string{
				"<tool_call>",
				`"name": "my_tool"`,
				`{"key":"val"}`,
				"</tool_call>",
				"<previous_response>",
			},
		},
		{
			name: "assistant role with tool_calls empty args",
			msgs: []openai.Message{
				{
					Role: "assistant",
					ToolCalls: []openai.ToolCall{
						{
							ID:   "call_xyz",
							Type: "function",
							Function: openai.ToolCallFunction{
								Name:      "no_args_tool",
								Arguments: "",
							},
						},
					},
				},
			},
			wantContains: []string{`{}`},
		},
		{
			name: "tool role",
			msgs: []openai.Message{
				{
					Role:       "tool",
					Name:       "search",
					ToolCallID: "call_123",
					Content:    rawString("result data"),
				},
			},
			wantContains: []string{
				`<tool_result name="search" tool_call_id="call_123">`,
				"result data",
				"</tool_result>",
			},
		},
		{
			name: "system empty content is skipped",
			msgs: []openai.Message{
				{Role: "system", Content: rawString("")},
				{Role: "user", Content: rawString("hi")},
			},
			wantSystem:   "",
			wantContains: []string{"User: hi"},
		},
		{
			name: "multiple user messages",
			msgs: []openai.Message{
				{Role: "user", Content: rawString("first")},
				{Role: "user", Content: rawString("second")},
			},
			wantContains: []string{"User: first", "User: second"},
		},
		{
			name: "system with content parts array",
			msgs: []openai.Message{
				{Role: "system", Content: func() json.RawMessage {
					parts := []partSpec{
						{Type: "text", Text: "System part 1."},
						{Type: "image", Text: "ignored"},
						{Type: "text", Text: "System part 2."},
					}
					b, _ := json.Marshal(parts)
					return b
				}()},
			},
			wantSystem: "System part 1.\nSystem part 2.",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Messages(tc.msgs)
			if tc.wantSystem != "" && got.SystemPrompt != tc.wantSystem {
				t.Errorf("SystemPrompt = %q, want %q", got.SystemPrompt, tc.wantSystem)
			}
			if tc.wantSystem == "" && len(tc.msgs) > 0 && tc.msgs[0].Role == "system" {
				// explicit empty system check
				if got.SystemPrompt != "" {
					t.Errorf("SystemPrompt = %q, want empty", got.SystemPrompt)
				}
			}
			for _, want := range tc.wantContains {
				if !strings.Contains(got.PromptText, want) {
					t.Errorf("PromptText %q does not contain %q", got.PromptText, want)
				}
			}
			for _, notWant := range tc.wantNotContain {
				if strings.Contains(got.PromptText, notWant) {
					t.Errorf("PromptText %q should not contain %q", got.PromptText, notWant)
				}
			}
		})
	}
}

// TestExtractContent covers nil, string, parts array, non-text parts skipped.
func TestExtractContent(t *testing.T) {
	type partSpec struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}

	tests := []struct {
		name string
		raw  json.RawMessage
		want string
	}{
		{
			name: "nil raw message",
			raw:  nil,
			want: "",
		},
		{
			name: "empty raw message",
			raw:  json.RawMessage{},
			want: "",
		},
		{
			name: "plain string",
			raw:  rawString("hello world"),
			want: "hello world",
		},
		{
			name: "empty string",
			raw:  rawString(""),
			want: "",
		},
		{
			name: "array single text part",
			raw: func() json.RawMessage {
				parts := []partSpec{{Type: "text", Text: "from parts"}}
				b, _ := json.Marshal(parts)
				return b
			}(),
			want: "from parts",
		},
		{
			name: "array multiple text parts joined with newline",
			raw: func() json.RawMessage {
				parts := []partSpec{
					{Type: "text", Text: "line one"},
					{Type: "text", Text: "line two"},
				}
				b, _ := json.Marshal(parts)
				return b
			}(),
			want: "line one\nline two",
		},
		{
			name: "array skips non-text parts",
			raw: func() json.RawMessage {
				parts := []partSpec{
					{Type: "image_url", Text: "http://img"},
					{Type: "text", Text: "only text"},
					{Type: "tool_use", Text: "ignored"},
				}
				b, _ := json.Marshal(parts)
				return b
			}(),
			want: "only text",
		},
		{
			name: "array all non-text parts returns empty",
			raw: func() json.RawMessage {
				parts := []partSpec{
					{Type: "image_url", Text: "http://img"},
				}
				b, _ := json.Marshal(parts)
				return b
			}(),
			want: "",
		},
		{
			name: "invalid json returns empty",
			raw:  json.RawMessage(`not-json`),
			want: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ExtractContent(tc.raw)
			if got != tc.want {
				t.Errorf("ExtractContent() = %q, want %q", got, tc.want)
			}
		})
	}
}
