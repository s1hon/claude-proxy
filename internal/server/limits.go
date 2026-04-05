package server

import "sync"

// Limiter enforces per-channel and global concurrent-request caps using
// buffered-channel semaphores.
type Limiter struct {
	global chan struct{}

	mu         sync.Mutex
	perChannel map[string]chan struct{}
	perLimit   int
}

// NewLimiter builds a limiter with the given caps.
func NewLimiter(global, perChannel int) *Limiter {
	if global <= 0 {
		global = 20
	}
	if perChannel <= 0 {
		perChannel = 2
	}
	return &Limiter{
		global:     make(chan struct{}, global),
		perChannel: make(map[string]chan struct{}),
		perLimit:   perChannel,
	}
}

// Acquire tries to reserve a global slot and a per-channel slot. If either is
// full it returns false immediately (non-blocking). On success, the returned
// release function MUST be called exactly once.
func (l *Limiter) Acquire(channelKey string) (release func(), ok bool) {
	// Global first (cheaper, fails fast when overloaded).
	select {
	case l.global <- struct{}{}:
	default:
		return nil, false
	}

	ch := l.channelSem(channelKey)
	select {
	case ch <- struct{}{}:
	default:
		<-l.global
		return nil, false
	}

	return func() {
		<-ch
		<-l.global
	}, true
}

func (l *Limiter) channelSem(key string) chan struct{} {
	l.mu.Lock()
	defer l.mu.Unlock()
	if ch, ok := l.perChannel[key]; ok {
		return ch
	}
	ch := make(chan struct{}, l.perLimit)
	l.perChannel[key] = ch
	return ch
}
