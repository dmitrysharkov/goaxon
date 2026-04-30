package event

import "context"

// OutboxEntry is a queued event row that has not yet been dispatched
// to the bus. The OutboxID is the store's internal identifier and is
// what Claim.Commit accepts; it has no domain meaning.
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
// invariant — see CLAUDE.md). Multiple dispatchers can run safely
// against the same Outbox: Claim's contract is that an entry returned
// to one caller will not be returned to another until Released.
type Outbox interface {
	// Claim opens a session that exclusively owns up to batchSize
	// pending entries. Returned entries are invisible to other Claim
	// calls until the claim is committed or released. The empty case
	// (no pending entries) returns a Claim whose Entries() is empty
	// and whose Commit/Release are no-ops; the caller still must call
	// one of them.
	Claim(ctx context.Context, batchSize int) (Claim, error)
}

// Claim is an in-flight processing handle for a batch of outbox
// entries. While a Claim is open, other Outbox.Claim calls will skip
// its entries (the Postgres implementation uses SELECT … FOR UPDATE
// SKIP LOCKED). Always end a Claim with Commit or Release — leaking
// one ties up a backend connection until the process dies.
type Claim interface {
	// Entries returns the locked entries in insertion order. The slice
	// is stable for the lifetime of the claim.
	Entries() []OutboxEntry

	// Commit marks the listed entries dispatched and releases the
	// lock. dispatchedIDs must be a subset of Entries(); pass an empty
	// slice to release the lock without marking anything (useful when
	// every publish in the batch failed).
	Commit(ctx context.Context, dispatchedIDs []int64) error

	// Release abandons the claim without marking anything dispatched.
	// The entries become available for re-claim by any dispatcher.
	Release(ctx context.Context) error
}
