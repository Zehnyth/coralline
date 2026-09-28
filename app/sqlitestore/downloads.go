package sqlitestore

import (
	"context"
	"log/slog"
	"time"

	"coralline/app/domain"
)

// DownloadAttributor buffers download events and flushes them in
// batches from a single background worker: terraform init fans out
// bursts of parallel downloads, and a synchronous insert per download
// would contend SQLite's single writer. Events are dropped under
// catastrophic overload rather than failing the actual download — an
// accepted, documented trade.
type DownloadAttributor struct {
	db     *DB
	events chan domain.DownloadEvent
	logger *slog.Logger
	done   chan struct{}
}

func NewDownloadAttributor(db *DB, logger *slog.Logger) *DownloadAttributor {
	a := &DownloadAttributor{
		db:     db,
		events: make(chan domain.DownloadEvent, 10_000),
		logger: logger,
		done:   make(chan struct{}),
	}
	go a.run()
	return a
}

// Close flushes buffered events and stops the worker. Call it only after
// the HTTP server has shut down: Record must not be called afterwards.
func (a *DownloadAttributor) Close() {
	close(a.events)
	<-a.done
}

// Record is non-blocking: if the buffer is full, the event is dropped
// and counted, never blocking the actual download response.
func (a *DownloadAttributor) Record(e domain.DownloadEvent) {
	select {
	case a.events <- e:
	default:
		a.logger.Warn("download attribution buffer full, dropping event",
			"namespace", e.Namespace, "name", e.Name)
	}
}

func (a *DownloadAttributor) run() {
	defer close(a.done)
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	var batch []domain.DownloadEvent
	flush := func() {
		if len(batch) == 0 {
			return
		}
		if err := a.db.insertDownloads(context.Background(), batch); err != nil {
			a.logger.Error("flushing download attribution batch failed", "error", err, "count", len(batch))
		}
		batch = batch[:0]
	}

	for {
		select {
		case e, ok := <-a.events:
			if !ok {
				flush()
				return
			}
			batch = append(batch, e)
			if len(batch) >= 500 {
				flush()
			}
		case <-ticker.C:
			flush()
		}
	}
}

func (db *DB) insertDownloads(ctx context.Context, events []domain.DownloadEvent) error {
	tx, err := db.sql.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx,
		`INSERT INTO downloads (namespace, name, system, version, token_id, downloaded_at) VALUES (?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, e := range events {
		if _, err := stmt.ExecContext(ctx, e.Namespace, e.Name, e.System, e.Version, e.TokenID,
			e.At.UTC().Format(time.RFC3339)); err != nil {
			return err
		}
	}
	return tx.Commit()
}
