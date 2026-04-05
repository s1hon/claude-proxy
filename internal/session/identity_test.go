package session

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

// TestExtractChannelLabel covers the Conversation info block parsing.
func TestExtractChannelLabel(t *testing.T) {
	type convInfo struct {
		Guild    string `json:"guild"`
		Channel  string `json:"channel"`
		Username string `json:"username"`
		DM       bool   `json:"dm"`
	}

	makeUserMsg := func(info convInfo) openai.Message {
		infoJSON, _ := json.Marshal(info)
		content := "Conversation info (untrusted metadata)\n" + string(infoJSON)
		return openai.Message{
			Role:    "user",
			Content: rawString(content),
		}
	}

	tests := []struct {
		name string
		msgs []openai.Message
		want string
	}{
		{
			name: "no user messages returns empty",
			msgs: []openai.Message{
				{Role: "assistant", Content: rawString("hello")},
			},
			want: "",
		},
		{
			name: "user message without conversation info returns empty",
			msgs: []openai.Message{
				{Role: "user", Content: rawString("just a question")},
			},
			want: "",
		},
		{
			name: "DM with username",
			msgs: []openai.Message{
				makeUserMsg(convInfo{DM: true, Username: "alice"}),
			},
			want: "dm:alice",
		},
		{
			name: "guild and channel",
			msgs: []openai.Message{
				makeUserMsg(convInfo{Guild: "MyServer", Channel: "general"}),
			},
			want: "MyServer #general",
		},
		{
			name: "channel only no guild",
			msgs: []openai.Message{
				makeUserMsg(convInfo{Channel: "random"}),
			},
			want: "#random",
		},
		{
			name: "username only no DM flag",
			msgs: []openai.Message{
				makeUserMsg(convInfo{Username: "bob"}),
			},
			want: "dm:bob",
		},
		{
			name: "only developer messages skipped",
			msgs: []openai.Message{
				{Role: "developer", Content: rawString("Conversation info (untrusted metadata)\n{\"channel\":\"dev\"}")},
			},
			want: "",
		},
		{
			name: "first matching user message wins",
			msgs: []openai.Message{
				makeUserMsg(convInfo{Guild: "First", Channel: "chan"}),
				makeUserMsg(convInfo{Guild: "Second", Channel: "chan2"}),
			},
			want: "First #chan",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ExtractChannelLabel(tc.msgs)
			if got != tc.want {
				t.Errorf("ExtractChannelLabel() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestExtractAgentName covers **Name:** extraction from developer/system messages.
func TestExtractAgentName(t *testing.T) {
	tests := []struct {
		name string
		msgs []openai.Message
		want string
	}{
		{
			name: "no messages returns default",
			msgs: []openai.Message{},
			want: "default",
		},
		{
			name: "user message ignored returns default",
			msgs: []openai.Message{
				{Role: "user", Content: rawString("**Name:** Alice")},
			},
			want: "default",
		},
		{
			name: "developer message with Name",
			msgs: []openai.Message{
				{Role: "developer", Content: rawString("**Name:** MyBot")},
			},
			want: "MyBot",
		},
		{
			name: "system message with Name",
			msgs: []openai.Message{
				{Role: "system", Content: rawString("**Name:** SysAgent")},
			},
			want: "SysAgent",
		},
		{
			name: "no Name line returns default",
			msgs: []openai.Message{
				{Role: "developer", Content: rawString("You are a helpful assistant.")},
			},
			want: "default",
		},
		{
			name: "Name with extra whitespace trimmed",
			msgs: []openai.Message{
				{Role: "developer", Content: rawString("**Name:**   Trimmed  ")},
			},
			want: "Trimmed",
		},
		{
			name: "first match wins",
			msgs: []openai.Message{
				{Role: "developer", Content: rawString("**Name:** First")},
				{Role: "system", Content: rawString("**Name:** Second")},
			},
			want: "First",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ExtractAgentName(tc.msgs)
			if got != tc.want {
				t.Errorf("ExtractAgentName() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestRoutingKey covers the key construction.
func TestRoutingKey(t *testing.T) {
	tests := []struct {
		name    string
		channel string
		agent   string
		want    string
	}{
		{
			name:    "both populated",
			channel: "MyServer #general",
			agent:   "MyBot",
			want:    "MyServer #general::MyBot",
		},
		{
			name:    "empty channel defaults to underscore",
			channel: "",
			agent:   "MyBot",
			want:    "_::MyBot",
		},
		{
			name:    "empty agent defaults to default",
			channel: "MyServer #general",
			agent:   "",
			want:    "MyServer #general::default",
		},
		{
			name:    "both empty",
			channel: "",
			agent:   "",
			want:    "_::default",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := RoutingKey(tc.channel, tc.agent)
			if got != tc.want {
				t.Errorf("RoutingKey(%q, %q) = %q, want %q", tc.channel, tc.agent, got, tc.want)
			}
		})
	}
}

// TestNewSessionID checks the UUID v4-like format.
func TestNewSessionID(t *testing.T) {
	for i := 0; i < 10; i++ {
		id := NewSessionID()

		// Must be 36 chars: 8-4-4-4-12 = 32 hex + 4 dashes.
		if len(id) != 36 {
			t.Errorf("NewSessionID() = %q, len=%d, want 36", id, len(id))
		}

		// Must contain exactly 4 dashes.
		if strings.Count(id, "-") != 4 {
			t.Errorf("NewSessionID() = %q, want 4 dashes", id)
		}

		// Segment lengths: 8-4-4-4-12.
		parts := strings.Split(id, "-")
		if len(parts) != 5 {
			t.Fatalf("NewSessionID() = %q, expected 5 segments", id)
		}
		wantLengths := []int{8, 4, 4, 4, 12}
		for j, wl := range wantLengths {
			if len(parts[j]) != wl {
				t.Errorf("segment[%d] = %q len=%d, want %d", j, parts[j], len(parts[j]), wl)
			}
		}

		// Version nibble (index 2, first char) should be '4'.
		if parts[2][0] != '4' {
			t.Errorf("version nibble = %c, want 4", parts[2][0])
		}
	}

	// Check that IDs are unique.
	seen := make(map[string]bool)
	for i := 0; i < 20; i++ {
		seen[NewSessionID()] = true
	}
	if len(seen) < 18 {
		t.Errorf("only %d unique IDs out of 20", len(seen))
	}
}
