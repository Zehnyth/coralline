package sqlitestore

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"
)

// LastUsedRecorder buffers token last-used timestamps and flushes them in
// coalesced batches from a single background worker — the same shape as
// DownloadAttributor, for the same reason: authentication runs on every
// request of a terraform init burst, and a synchronous UPDATE per request
// would contend SQLite's single writer. Updates are dropped under overload
// rather than slowing authentication; last-used is informational.
type LastUsedRecorder struct {
	db      *DB
	events  chan lastUsedEvent
	logger  *slog.Logger
	dropped atomic.Int64
	done    chan struct{}
}

type lastUsedEvent struct {
	tokenID string
	at      time.Time
}

func NewLastUsedRecorder(db *DB, logger *slog.Logger) *LastUsedRecorder {
	r := &LastUsedRecorder{
		db:     db,
		events: make(chan lastUsedEvent, 10_000),
		logger: logger,
		done:   make(chan struct{}),
	}
	go r.run()
	return r
}

// Close flushes pending updates and stops the worker. Call it only after
// the HTTP servers have shut down: Record must not be called afterwards.
func (r *LastUsedRecorder) Close() {
	close(r.events)
	<-r.done
}

// Record is non-blocking.
func (r *LastUsedRecorder) Record(tokenID string, at time.Time) {
	select {
	case r.events <- lastUsedEvent{tokenID: tokenID, at: at}:
	default:
		r.dropped.Add(1)
	}
}

func (r *LastUsedRecorder) run() {
	defer close(r.done)
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	// Coalesce to one write per token per flush, however many requests it made.
	pending := map[string]time.Time{}
	flush := func() {
		if n := r.dropped.Swap(0); n > 0 {
			r.logger.Warn("token last-used buffer full, dropped updates", "count", n)
		}
		if len(pending) == 0 {
			return
		}
		if err := r.db.touchLastUsed(context.Background(), pending); err != nil {
			r.logger.Error("flushing token last-used batch failed", "error", err, "count", len(pending))
		}
		pending = map[string]time.Time{}
	}

	for {
		select {
		case e, ok := <-r.events:
			if !ok {
				flush()
				return
			}
			if prev, ok := pending[e.tokenID]; !ok || e.at.After(prev) {
				pending[e.tokenID] = e.at
			}
		case <-ticker.C:
			flush()
		}
	}
}
