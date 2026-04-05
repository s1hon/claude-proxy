package session

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestNewStore verifies an empty store is usable immediately.
func TestNewStore(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	s := NewStore(path)
	if s == nil {
		t.Fatal("NewStore returned nil")
	}

	if _, ok := s.GetByChannel("any"); ok {
		t.Error("new store should have empty channelMap")
	}
	if _, ok := s.GetByResponseKey("any"); ok {
		t.Error("new store should have empty responseMap")
	}
}

// TestSetAndGetByChannel covers upsert and lookup.
func TestSetAndGetByChannel(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(filepath.Join(dir, "state.json"))

	key := "MyServer #general::MyBot"
	sessionID := "sess-001"

	// Not found initially.
	if _, ok := s.GetByChannel(key); ok {
		t.Fatal("unexpected entry before set")
	}

	s.SetChannel(key, sessionID)

	entry, ok := s.GetByChannel(key)
	if !ok {
		t.Fatal("entry not found after SetChannel")
	}
	if entry.SessionID != sessionID {
		t.Errorf("SessionID = %q, want %q", entry.SessionID, sessionID)
	}
	if entry.CreatedAt == 0 {
		t.Error("CreatedAt should be set")
	}
	if entry.UpdatedAt == 0 {
		t.Error("UpdatedAt should be set")
	}
}

// TestSetChannelUpsertSameID verifies UpdatedAt advances on re-set with same ID.
func TestSetChannelUpsertSameID(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(filepath.Join(dir, "state.json"))

	key := "ch::agent"
	s.SetChannel(key, "sess-1")
	first, _ := s.GetByChannel(key)

	// Set again with same ID.
	s.SetChannel(key, "sess-1")
	second, _ := s.GetByChannel(key)

	if second.CreatedAt != first.CreatedAt {
		t.Errorf("CreatedAt changed on re-set: was %d, now %d", first.CreatedAt, second.CreatedAt)
	}
}

// TestSetAndGetByResponseKey covers response key upsert and lookup.
func TestSetAndGetByResponseKey(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(filepath.Join(dir, "state.json"))

	text := "This is the assistant response text."
	sessionID := "sess-resp-1"

	s.SetResponseKey(text, sessionID)

	entry, ok := s.GetByResponseKey(text)
	if !ok {
		t.Fatal("entry not found after SetResponseKey")
	}
	if entry.SessionID != sessionID {
		t.Errorf("SessionID = %q, want %q", entry.SessionID, sessionID)
	}
}

// TestResponseKeyTruncation verifies keys over 200 chars are truncated.
func TestResponseKeyTruncation(t *testing.T) {
	long := strings.Repeat("a", 300)
	key := ResponseKey(long)
	if len(key) != 200 {
		t.Errorf("ResponseKey len = %d, want 200", len(key))
	}

	short := "hello"
	key2 := ResponseKey(short)
	if key2 != short {
		t.Errorf("ResponseKey(%q) = %q, want unchanged", short, key2)
	}

	exact := strings.Repeat("b", 200)
	key3 := ResponseKey(exact)
	if key3 != exact {
		t.Errorf("ResponseKey of exactly 200 chars should be unchanged")
	}

	if ResponseKey("") != "" {
		t.Error("ResponseKey(\"\") should return empty")
	}
}

// TestResponseKeyUsedForLookup verifies SetResponseKey truncates on store side.
func TestResponseKeyUsedForLookup(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(filepath.Join(dir, "state.json"))

	long := strings.Repeat("z", 300)
	s.SetResponseKey(long, "sess-long")

	// Lookup by truncated key should work.
	truncated := long[:200]
	entry, ok := s.GetByResponseKey(truncated)
	if !ok {
		t.Fatal("entry not found using truncated key")
	}
	if entry.SessionID != "sess-long" {
		t.Errorf("SessionID = %q, want sess-long", entry.SessionID)
	}
}

// TestSaveAndLoad verifies round-trip persistence.
func TestSaveAndLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")

	s1 := NewStore(path)
	s1.SetChannel("ch::agent", "sess-ch")
	s1.SetResponseKey("response text", "sess-resp")

	if err := s1.Save(); err != nil {
		t.Fatalf("Save() error: %v", err)
	}

	s2 := NewStore(path)
	err := s2.Load(func(id string) bool { return true })
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	entry, ok := s2.GetByChannel("ch::agent")
	if !ok {
		t.Fatal("channel entry not found after load")
	}
	if entry.SessionID != "sess-ch" {
		t.Errorf("SessionID = %q, want sess-ch", entry.SessionID)
	}

	rEntry, ok := s2.GetByResponseKey("response text")
	if !ok {
		t.Fatal("response entry not found after load")
	}
	if rEntry.SessionID != "sess-resp" {
		t.Errorf("SessionID = %q, want sess-resp", rEntry.SessionID)
	}
}

// TestLoadMissingFileIsNoop verifies Load returns nil when file doesn't exist.
func TestLoadMissingFileIsNoop(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nonexistent.json")
	s := NewStore(path)
	if err := s.Load(nil); err != nil {
		t.Errorf("Load() on missing file returned error: %v", err)
	}
}

// TestLoadPrunesNonExistentSessions verifies sessionExists=false causes pruning.
func TestLoadPrunesNonExistentSessions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")

	// Seed state with two sessions.
	s1 := NewStore(path)
	s1.SetChannel("ch::a1", "sess-alive")
	s1.SetChannel("ch::a2", "sess-dead")

	if err := s1.Save(); err != nil {
		t.Fatalf("Save() error: %v", err)
	}

	// Load with a predicate that only allows "sess-alive".
	s2 := NewStore(path)
	err := s2.Load(func(id string) bool {
		return id == "sess-alive"
	})
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if _, ok := s2.GetByChannel("ch::a1"); !ok {
		t.Error("alive session was pruned unexpectedly")
	}
	if _, ok := s2.GetByChannel("ch::a2"); ok {
		t.Error("dead session should have been pruned")
	}
}

// TestDeleteByChannel removes a channel entry.
func TestDeleteByChannel(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(filepath.Join(dir, "state.json"))

	s.SetChannel("ch::agent", "sess-1")
	s.DeleteByChannel("ch::agent")

	if _, ok := s.GetByChannel("ch::agent"); ok {
		t.Error("entry should be deleted")
	}
}
