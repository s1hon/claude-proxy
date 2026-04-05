package session

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Entry records one session mapping.
type Entry struct {
	SessionID           string `json:"session_id"`
	CreatedAt           int64  `json:"created_at"`
	UpdatedAt           int64  `json:"updated_at"`
	LastCompactionHash  int32  `json:"last_compaction_hash,omitempty"`
}

// Store is an in-memory session map with persistence to state.json.
// Two tiers are tracked:
//   - channelMap keyed by "channel::agent" (primary)
//   - responseMap keyed by the first 200 chars of the last assistant response
//     (fallback for when no channel metadata is available)
type Store struct {
	path string

	mu          sync.RWMutex
	channelMap  map[string]Entry
	responseMap map[string]Entry

	// keyLocks serialises concurrent requests that target the same routing
	// key. Without this, two simultaneous refresh decisions race on
	// LastCompactionHash and both allocate fresh session IDs — one of them
	// is then discarded when Save() runs. Held for the full handler
	// lifetime, not just storage writes.
	keyLocks sync.Map // map[string]*sync.Mutex
}

// persisted is the on-disk state.json schema.
type persisted struct {
	ChannelMap  map[string]Entry `json:"channel_map"`
	ResponseMap map[string]Entry `json:"response_map"`
}

// NewStore creates an empty store bound to the given file path.
func NewStore(path string) *Store {
	return &Store{
		path:        path,
		channelMap:  make(map[string]Entry),
		responseMap: make(map[string]Entry),
	}
}

// Load reads state.json and prunes entries whose CLI session file no longer exists.
// Returns nil if the file is missing.
func (s *Store) Load(sessionExists func(id string) bool) error {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var p persisted
	if err := json.Unmarshal(data, &p); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if p.ChannelMap != nil {
		for k, v := range p.ChannelMap {
			if sessionExists == nil || sessionExists(v.SessionID) {
				s.channelMap[k] = v
			}
		}
	}
	if p.ResponseMap != nil {
		for k, v := range p.ResponseMap {
			if sessionExists == nil || sessionExists(v.SessionID) {
				s.responseMap[k] = v
			}
		}
	}
	return nil
}

// Save writes the current maps to state.json atomically (tmp + rename).
func (s *Store) Save() error {
	s.mu.RLock()
	p := persisted{
		ChannelMap:  copyMap(s.channelMap),
		ResponseMap: copyMap(s.responseMap),
	}
	s.mu.RUnlock()

	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(s.path)
	if dir == "" {
		dir = "."
	}
	tmp, err := os.CreateTemp(dir, ".state-*.json.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return err
	}
	return os.Rename(tmpPath, s.path)
}

// GetByChannel returns the entry for a routing key, if any.
func (s *Store) GetByChannel(key string) (Entry, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	e, ok := s.channelMap[key]
	return e, ok
}

// GetByResponseKey returns the entry keyed by a response-text snippet.
func (s *Store) GetByResponseKey(k string) (Entry, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	e, ok := s.responseMap[k]
	return e, ok
}

// SetChannel upserts a channel entry and updates timestamps. When the session
// id changes, LastCompactionHash is reset; when only the timestamp changes it
// is preserved.
func (s *Store) SetChannel(key, sessionID string) {
	now := time.Now().Unix()
	s.mu.Lock()
	defer s.mu.Unlock()
	existing, ok := s.channelMap[key]
	if !ok || existing.SessionID != sessionID {
		s.channelMap[key] = Entry{SessionID: sessionID, CreatedAt: now, UpdatedAt: now}
		return
	}
	existing.UpdatedAt = now
	s.channelMap[key] = existing
}

// SetCompactionHash records the compaction hash for a channel entry so future
// requests can detect when the upstream has re-compacted.
func (s *Store) SetCompactionHash(key string, hash int32) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.channelMap[key]
	if !ok {
		return
	}
	e.LastCompactionHash = hash
	e.UpdatedAt = time.Now().Unix()
	s.channelMap[key] = e
}

// SetResponseKey upserts a response-key entry (keyed by first 200 chars).
func (s *Store) SetResponseKey(text, sessionID string) {
	key := ResponseKey(text)
	if key == "" {
		return
	}
	now := time.Now().Unix()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.responseMap[key] = Entry{SessionID: sessionID, CreatedAt: now, UpdatedAt: now}
}

// LockKey returns an unlock function after acquiring the per-routing-key
// mutex. Callers MUST invoke the returned function exactly once, typically
// with defer. This guarantees that refresh detection, session-ID allocation,
// CLI invocation and persistence for a single routing key run serially even
// when multiple concurrent HTTP requests land on the same bucket.
func (s *Store) LockKey(key string) func() {
	actual, _ := s.keyLocks.LoadOrStore(key, &sync.Mutex{})
	mu := actual.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

// DeleteByChannel removes a channel entry.
func (s *Store) DeleteByChannel(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.channelMap, key)
}

// ResponseKey derives the lookup key from an assistant response text.
func ResponseKey(text string) string {
	if len(text) > 200 {
		return text[:200]
	}
	return text
}

func copyMap(m map[string]Entry) map[string]Entry {
	out := make(map[string]Entry, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
