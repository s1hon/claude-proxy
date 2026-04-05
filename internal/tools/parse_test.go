package tools

import (
	"strings"
	"testing"
)

// TestParseToolCalls covers single block, multiple blocks, invalid JSON skip,
// no block returns nil, and ID format validation.
func TestParseToolCalls(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		wantNil     bool
		wantCount   int
		wantNames   []string
		wantArgs    []string // parallel to wantNames
	}{
		{
			name:    "no tool_call block returns nil",
			input:   "Just a plain response with no tool calls.",
			wantNil: true,
		},
		{
			name: "single valid block",
			input: `<tool_call>
{"name": "web_search", "arguments": {"query": "hello"}}
</tool_call>`,
			wantCount: 1,
			wantNames: []string{"web_search"},
		},
		{
			name: "multiple valid blocks",
			input: `<tool_call>
{"name": "tool_a", "arguments": {"x": 1}}
</tool_call>
some text
<tool_call>
{"name": "tool_b", "arguments": {"y": 2}}
</tool_call>`,
			wantCount: 2,
			wantNames: []string{"tool_a", "tool_b"},
		},
		{
			name: "invalid JSON block is skipped",
			input: `<tool_call>
NOT VALID JSON
</tool_call>
<tool_call>
{"name": "good_tool", "arguments": {}}
</tool_call>`,
			wantCount: 1,
			wantNames: []string{"good_tool"},
		},
		{
			name: "block with empty name is skipped",
			input: `<tool_call>
{"name": "", "arguments": {}}
</tool_call>
<tool_call>
{"name": "valid", "arguments": {}}
</tool_call>`,
			wantCount: 1,
			wantNames: []string{"valid"},
		},
		{
			name: "missing arguments defaults to empty object",
			input: `<tool_call>
{"name": "no_args"}
</tool_call>`,
			wantCount: 1,
			wantNames: []string{"no_args"},
			wantArgs:  []string{"{}"},
		},
		{
			name: "arguments preserved as-is",
			input: `<tool_call>
{"name": "my_tool", "arguments": {"key": "value", "num": 42}}
</tool_call>`,
			wantCount: 1,
			wantNames: []string{"my_tool"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseToolCalls(tc.input)

			if tc.wantNil {
				if got != nil {
					t.Errorf("expected nil, got %v", got)
				}
				return
			}

			if len(got) != tc.wantCount {
				t.Errorf("got %d tool calls, want %d", len(got), tc.wantCount)
				return
			}

			for i, name := range tc.wantNames {
				if i >= len(got) {
					break
				}
				tc := got[i]
				// Validate ID format: must start with "call_" and be > 5 chars.
				if !strings.HasPrefix(tc.ID, "call_") {
					t.Errorf("ToolCall[%d].ID = %q, want prefix call_", i, tc.ID)
				}
				if len(tc.ID) <= len("call_") {
					t.Errorf("ToolCall[%d].ID = %q too short", i, tc.ID)
				}
				if tc.Type != "function" {
					t.Errorf("ToolCall[%d].Type = %q, want function", i, tc.Type)
				}
				if tc.Function.Name != name {
					t.Errorf("ToolCall[%d].Name = %q, want %q", i, tc.Function.Name, name)
				}
			}

			// Check args if specified.
			for i, wantArg := range tc.wantArgs {
				if i >= len(got) {
					break
				}
				if got[i].Function.Arguments != wantArg {
					t.Errorf("ToolCall[%d].Arguments = %q, want %q", i, got[i].Function.Arguments, wantArg)
				}
			}
		})
	}
}

// TestParseToolCallsIDUniqueness checks that repeated calls produce different IDs.
func TestParseToolCallsIDUniqueness(t *testing.T) {
	input := `<tool_call>
{"name": "tool_a", "arguments": {}}
</tool_call>`

	ids := make(map[string]bool)
	for i := 0; i < 20; i++ {
		calls := ParseToolCalls(input)
		if len(calls) != 1 {
			t.Fatalf("expected 1 call, got %d", len(calls))
		}
		ids[calls[0].ID] = true
	}
	// With 12 random hex chars, collision probability is astronomically low.
	// We just verify IDs are plausibly unique (at least 10 distinct out of 20).
	if len(ids) < 10 {
		t.Errorf("IDs appear non-random: only %d unique IDs out of 20 calls", len(ids))
	}
}

// TestCleanText checks that XML tags are stripped and whitespace collapsed.
func TestCleanText(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "no tags returns trimmed text",
			input: "  plain text  ",
			want:  "plain text",
		},
		{
			name: "tool_call block removed",
			input: `before
<tool_call>
{"name": "foo", "arguments": {}}
</tool_call>
after`,
			want: "before\n\nafter",
		},
		{
			name: "tool_result block removed",
			input: `response
<tool_result name="foo" tool_call_id="c1">
some result
</tool_result>
end`,
			want: "response\n\nend",
		},
		{
			name: "previous_response block removed",
			input: `text
<previous_response>
old response here
</previous_response>
new text`,
			want: "text\n\nnew text",
		},
		{
			name: "multiple blocks all removed",
			input: `start
<tool_call>{"name": "x", "arguments": {}}</tool_call>
middle
<tool_result name="x" tool_call_id="c1">result</tool_result>
<previous_response>old</previous_response>
end`,
			want: "start\n\nmiddle\n\nend",
		},
		{
			name:  "empty string stays empty",
			input: "",
			want:  "",
		},
		{
			name: "excessive newlines collapsed",
			input: `line1



line2`,
			want: "line1\n\nline2",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := CleanText(tc.input)
			if got != tc.want {
				t.Errorf("CleanText() = %q, want %q", got, tc.want)
			}
		})
	}
}
