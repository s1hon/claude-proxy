// Package stats holds process-wide counters updated atomically.
package stats

import (
	"sync/atomic"
	"time"
)

// Stats tracks aggregate runtime metrics.
type Stats struct {
	StartedAt      time.Time
	TotalRequests  atomic.Int64
	ActiveRequests atomic.Int64
	Errors         atomic.Int64
}

// New returns a Stats with StartedAt set to now.
func New() *Stats {
	return &Stats{StartedAt: time.Now()}
}

// Snapshot is a point-in-time copy safe for JSON serialisation.
type Snapshot struct {
	UptimeSeconds  int64 `json:"uptime_seconds"`
	TotalRequests  int64 `json:"total_requests"`
	ActiveRequests int64 `json:"active_requests"`
	Errors         int64 `json:"errors"`
}

// Snapshot returns the current counters.
func (s *Stats) Snapshot() Snapshot {
	return Snapshot{
		UptimeSeconds:  int64(time.Since(s.StartedAt).Seconds()),
		TotalRequests:  s.TotalRequests.Load(),
		ActiveRequests: s.ActiveRequests.Load(),
		Errors:         s.Errors.Load(),
	}
}
