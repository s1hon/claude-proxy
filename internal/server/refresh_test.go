package server

import (
	"encoding/json"
	"testing"

	"github.com/s1hon/claude-proxy/internal/openai"
)

func raw(s string) json.RawMessage {
	b, _ := json.Marshal(s)
	return b
}

func TestDetectCompactionHash_Present(t *testing.T) {
	msgs := []openai.Message{
		{Role: "developer", Content: raw("**Name:** Bot")},
		{Role: "user", Content: raw(compactionPrefix + " here is the summary of earlier turns...")},
	}
	hash, ok := detectCompactionHash(msgs)
	if !ok {
		t.Fatal("expected compaction hash to be detected")
	}
	if hash == 0 {
		t.Fatal("hash should be non-zero for non-empty prefix")
	}
}

func TestDetectCompactionHash_Absent(t *testing.T) {
	msgs := []openai.Message{
		{Role: "user", Content: raw("hello, this is a normal message")},
	}
	if _, ok := detectCompactionHash(msgs); ok {
		t.Fatal("expected no hash")
	}
}

func TestDetectCompactionHash_OnlyUserMessagesScanned(t *testing.T) {
	msgs := []openai.Message{
		{Role: "system", Content: raw(compactionPrefix + " xxx")}, // must be ignored
	}
	if _, ok := detectCompactionHash(msgs); ok {
		t.Fatal("system/developer messages should not trigger compaction detection")
	}
}

func TestDetectCompactionHash_SnippetBounded(t *testing.T) {
	// Two user messages with different suffixes beyond the 500-char window
	// must produce the same hash.
	a := compactionPrefix + " " + repeatChar('a', 1000)
	b := compactionPrefix + " " + repeatChar('a', 500) + repeatChar('b', 500)

	ha, _ := detectCompactionHash([]openai.Message{{Role: "user", Content: raw(a)}})
	hb, _ := detectCompactionHash([]openai.Message{{Role: "user", Content: raw(b)}})
	if ha != hb {
		// Same first 500 chars → same hash
		t.Errorf("expected identical hashes for identical prefix-500, got %d vs %d", ha, hb)
	}
}

func TestDetectCompactionHash_DifferentContentDifferentHash(t *testing.T) {
	a := compactionPrefix + " summary A"
	b := compactionPrefix + " summary B"
	ha, _ := detectCompactionHash([]openai.Message{{Role: "user", Content: raw(a)}})
	hb, _ := detectCompactionHash([]openai.Message{{Role: "user", Content: raw(b)}})
	if ha == hb {
		t.Errorf("expected different hashes, both got %d", ha)
	}
}

func TestJavaStringHash_Deterministic(t *testing.T) {
	if javaStringHash("hello") != javaStringHash("hello") {
		t.Fatal("hash not deterministic")
	}
	if javaStringHash("a") == javaStringHash("b") {
		t.Fatal("different inputs should produce different hashes")
	}
}

func repeatChar(c byte, n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = c
	}
	return string(b)
}
