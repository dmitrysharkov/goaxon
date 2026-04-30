package event

import "context"

// OutboxEntry is a queued event row that has not yet been dispatched
// to the bus. The OutboxID is the store's internal identifier and is
// what MarkDispatched accepts; it has no domain meaning.
type OutboxEntry struct {
	OutboxID int64
	Envelope Envelope
}

// Outbox is implemented by stores that persist events and a dispatch
// queue in a single transaction. The pattern guarantees that a crash
// between "events written" and "events published" can't lose events:
// the outbox row commits with the event, and a separate dispatcher
// process publishes it later.
//
// When a Repository's Store also satisfies Outbox, Save skips its
// synchronous bus.Publish loop — publication becomes the dispatcher's
// responsibility. Implementations MUST keep the outbox insert in the
// same transaction as the event append; otherwise the guarantee
// dissolves and you have the worst of both worlds.
//
// Delivery is at-least-once; handlers must be idempotent (a goaxon-wide
// invariant — see CLAUDE.md). A single dispatcher is sufficient for v1.
// Running several requires SELECT ... FOR UPDATE SKIP LOCKED in
// LoadPending, which we have not added yet.
type Outbox interface {
	// LoadPending returns up to batchSize undispatched entries in
	// insertion order. An empty slice means the queue is drained.
	LoadPending(ctx context.Context, batchSize int) ([]OutboxEntry, error)

	// MarkDispatched flags the listed entries as published. Implementations
	// are expected to set a dispatched_at timestamp rather than delete
	// rows, so retention/audit is the operator's choice.
	MarkDispatched(ctx context.Context, outboxIDs []int64) error
}
