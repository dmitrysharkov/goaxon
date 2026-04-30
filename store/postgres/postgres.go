// Package postgres provides a Postgres-backed implementation of
// event.Store.
//
// Use it for any deployment where the in-memory store won't do — i.e.,
// anything where events need to survive process restarts. Pair it with
// the outbox dispatcher (see Repository docs) so handlers don't get
// orphaned by a crash between append and publish.
package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/dmitrysharkov/goaxon/event"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Schema is the DDL the store expects. Apply it once at deploy time
// (or via your migration tool of choice). The CREATE statements are
// idempotent so re-running is safe.
//
// Two tables: events is the immutable system of record (replay reads
// from here); outbox is the dispatch queue (a row per event, written
// in the same transaction as the event itself). Once the dispatcher
// has published an entry it sets dispatched_at — rows are not deleted
// so operators can audit / re-dispatch if needed.
//
// aggregate_id is uuid (the framework's AggregateID is uuid.UUID); UUIDv7
// is recommended at the application level so the time-ordered prefix
// gives the events table good B-tree locality.
const Schema = `
CREATE TABLE IF NOT EXISTS events (
    aggregate_id    uuid        NOT NULL,
    aggregate_type  text        NOT NULL,
    sequence        bigint      NOT NULL,
    event_type      text        NOT NULL,
    payload         jsonb       NOT NULL,
    metadata        jsonb       NOT NULL DEFAULT '{}'::jsonb,
    occurred_at     timestamptz NOT NULL,
    PRIMARY KEY (aggregate_id, sequence)
);

CREATE TABLE IF NOT EXISTS outbox (
    id              bigserial   PRIMARY KEY,
    aggregate_id    uuid        NOT NULL,
    aggregate_type  text        NOT NULL,
    sequence        bigint      NOT NULL,
    event_type      text        NOT NULL,
    payload         jsonb       NOT NULL,
    metadata        jsonb       NOT NULL DEFAULT '{}'::jsonb,
    occurred_at     timestamptz NOT NULL,
    dispatched_at   timestamptz
);
CREATE INDEX IF NOT EXISTS outbox_pending ON outbox (id) WHERE dispatched_at IS NULL;
`

// Store implements event.Store on top of a Postgres connection pool.
// It's safe for concurrent use; the optimistic-concurrency check is
// enforced by the (aggregate_id, sequence) primary key.
type Store struct {
	pool     *pgxpool.Pool
	registry *event.Registry
}

var (
	_ event.Store  = (*Store)(nil)
	_ event.Outbox = (*Store)(nil)
)

// NewStore returns a Store backed by pool. The registry is consulted
// during Load to reconstruct concrete Event values from JSON; every
// event type your aggregates produce must be registered before Load
// is called.
func NewStore(pool *pgxpool.Pool, registry *event.Registry) *Store {
	return &Store{pool: pool, registry: registry}
}

// Append implements event.Store. It writes events in a single
// transaction. Concurrency conflicts surface as event.ErrConcurrencyConflict
// when either the head check fails (caller's expectedVersion is stale)
// or a unique-constraint violation fires (a concurrent appender beat us).
func (s *Store) Append(ctx context.Context, aggregateID uuid.UUID, expectedVersion uint64, events []event.Envelope) error {
	if len(events) == 0 {
		return nil
	}
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var head int64
		err := tx.QueryRow(ctx,
			`SELECT COALESCE(MAX(sequence), 0) FROM events WHERE aggregate_id = $1`,
			[16]byte(aggregateID),
		).Scan(&head)
		if err != nil {
			return fmt.Errorf("postgres: head check: %w", err)
		}
		if uint64(head) != expectedVersion {
			return event.ErrConcurrencyConflict
		}
		for _, env := range events {
			if err := insertEvent(ctx, tx, env); err != nil {
				return err
			}
		}
		return nil
	})
}

func insertEvent(ctx context.Context, tx pgx.Tx, env event.Envelope) error {
	payload, err := json.Marshal(env.Payload)
	if err != nil {
		return fmt.Errorf("postgres: marshal payload %s: %w", env.Payload.EventType(), err)
	}
	metadata, err := json.Marshal(env.Metadata)
	if err != nil {
		return fmt.Errorf("postgres: marshal metadata: %w", err)
	}
	idBytes := [16]byte(env.AggregateID)
	_, err = tx.Exec(ctx, `
        INSERT INTO events (aggregate_id, aggregate_type, sequence, event_type, payload, metadata, occurred_at)
        VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		idBytes, env.AggregateType, int64(env.Sequence),
		env.Payload.EventType(), payload, metadata, env.Timestamp,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return event.ErrConcurrencyConflict
		}
		return fmt.Errorf("postgres: insert event: %w", err)
	}
	_, err = tx.Exec(ctx, `
        INSERT INTO outbox (aggregate_id, aggregate_type, sequence, event_type, payload, metadata, occurred_at)
        VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		idBytes, env.AggregateType, int64(env.Sequence),
		env.Payload.EventType(), payload, metadata, env.Timestamp,
	)
	if err != nil {
		return fmt.Errorf("postgres: insert outbox: %w", err)
	}
	return nil
}

// Claim implements event.Outbox. It opens a transaction, locks up to
// batchSize undispatched rows with SELECT … FOR UPDATE SKIP LOCKED,
// and returns a Claim handle that owns the transaction until Commit
// or Release. Other concurrent Claim calls will skip the locked rows
// (the SKIP LOCKED clause), so multiple dispatchers can run safely
// against the same outbox.
//
// The empty case (no pending rows) returns a Claim with an empty
// Entries() slice and a nil tx; its Commit/Release are no-ops.
func (s *Store) Claim(ctx context.Context, batchSize int) (event.Claim, error) {
	if batchSize <= 0 {
		return &claim{}, nil
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("postgres: claim begin: %w", err)
	}
	rows, err := tx.Query(ctx, `
        SELECT id, aggregate_id, aggregate_type, sequence, event_type, payload, metadata, occurred_at
        FROM outbox
        WHERE dispatched_at IS NULL
        ORDER BY id
        LIMIT $1
        FOR UPDATE SKIP LOCKED`,
		batchSize,
	)
	if err != nil {
		_ = tx.Rollback(ctx)
		return nil, fmt.Errorf("postgres: claim query: %w", err)
	}
	entries, scanErr := scanOutboxRows(rows, s.registry)
	rows.Close()
	if scanErr != nil {
		_ = tx.Rollback(ctx)
		return nil, scanErr
	}
	if len(entries) == 0 {
		// Nothing to do — close the tx so we don't park a connection.
		if err := tx.Rollback(ctx); err != nil {
			return nil, fmt.Errorf("postgres: claim rollback empty: %w", err)
		}
		return &claim{}, nil
	}
	return &claim{tx: tx, entries: entries}, nil
}

// claim is the Postgres-side implementation of event.Claim. It owns the
// pgx.Tx that holds the row locks until Commit or Release ends it.
type claim struct {
	tx      pgx.Tx
	entries []event.OutboxEntry
}

func (c *claim) Entries() []event.OutboxEntry { return c.entries }

func (c *claim) Commit(ctx context.Context, ids []int64) error {
	if c.tx == nil {
		return nil
	}
	if len(ids) > 0 {
		if _, err := c.tx.Exec(ctx,
			`UPDATE outbox SET dispatched_at = now() WHERE id = ANY($1)`, ids,
		); err != nil {
			_ = c.tx.Rollback(ctx)
			return fmt.Errorf("postgres: claim mark dispatched: %w", err)
		}
	}
	if err := c.tx.Commit(ctx); err != nil {
		return fmt.Errorf("postgres: claim commit: %w", err)
	}
	return nil
}

func (c *claim) Release(ctx context.Context) error {
	if c.tx == nil {
		return nil
	}
	if err := c.tx.Rollback(ctx); err != nil {
		return fmt.Errorf("postgres: claim release: %w", err)
	}
	return nil
}

func scanOutboxRows(rows pgx.Rows, registry *event.Registry) ([]event.OutboxEntry, error) {
	var out []event.OutboxEntry
	for rows.Next() {
		var (
			outboxID             int64
			aggIDBytes           [16]byte
			aggType, eventType   string
			sequence             int64
			payload, metadataRaw []byte
			occurredAt           time.Time
		)
		if err := rows.Scan(&outboxID, &aggIDBytes, &aggType, &sequence, &eventType, &payload, &metadataRaw, &occurredAt); err != nil {
			return nil, fmt.Errorf("postgres: outbox scan: %w", err)
		}
		evt, err := registry.Unmarshal(eventType, payload)
		if err != nil {
			return nil, err
		}
		var metadata map[string]string
		if len(metadataRaw) > 0 && string(metadataRaw) != "null" {
			if err := json.Unmarshal(metadataRaw, &metadata); err != nil {
				return nil, fmt.Errorf("postgres: outbox metadata: %w", err)
			}
		}
		out = append(out, event.OutboxEntry{
			OutboxID: outboxID,
			Envelope: event.Envelope{
				AggregateID:   uuid.UUID(aggIDBytes),
				AggregateType: aggType,
				Sequence:      uint64(sequence),
				Timestamp:     occurredAt,
				Payload:       evt,
				Metadata:      metadata,
			},
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: outbox rows: %w", err)
	}
	return out, nil
}

// Load implements event.Store. Events are returned in sequence order.
// Returns (nil, nil) for an unknown stream — the same convention the
// in-memory store uses, so the aggregate repository can start from a
// fresh aggregate without a special "not found" branch.
func (s *Store) Load(ctx context.Context, aggregateID uuid.UUID) ([]event.Envelope, error) {
	rows, err := s.pool.Query(ctx, `
        SELECT aggregate_id, aggregate_type, sequence, event_type, payload, metadata, occurred_at
        FROM events
        WHERE aggregate_id = $1
        ORDER BY sequence`,
		[16]byte(aggregateID),
	)
	if err != nil {
		return nil, fmt.Errorf("postgres: query: %w", err)
	}
	defer rows.Close()

	var out []event.Envelope
	for rows.Next() {
		var (
			aggIDBytes           [16]byte
			aggType, eventType   string
			sequence             int64
			payload, metadataRaw []byte
			occurredAt           time.Time
		)
		if err := rows.Scan(&aggIDBytes, &aggType, &sequence, &eventType, &payload, &metadataRaw, &occurredAt); err != nil {
			return nil, fmt.Errorf("postgres: scan: %w", err)
		}
		evt, err := s.registry.Unmarshal(eventType, payload)
		if err != nil {
			return nil, err
		}
		var metadata map[string]string
		if len(metadataRaw) > 0 && string(metadataRaw) != "null" {
			if err := json.Unmarshal(metadataRaw, &metadata); err != nil {
				return nil, fmt.Errorf("postgres: unmarshal metadata: %w", err)
			}
		}
		out = append(out, event.Envelope{
			AggregateID:   uuid.UUID(aggIDBytes),
			AggregateType: aggType,
			Sequence:      uint64(sequence),
			Timestamp:     occurredAt,
			Payload:       evt,
			Metadata:      metadata,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: rows: %w", err)
	}
	return out, nil
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
