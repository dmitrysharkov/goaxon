// Package event defines the core event abstractions used across goaxon.
//
// An Event is anything that has happened in the past — immutable, named,
// and carrying enough data to rebuild state from a stream of them.
package event

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// Event is the marker interface every domain event must satisfy.
// Keep events small, immutable, and named in past tense (OrderPlaced,
// not PlaceOrder — that would be a command).
type Event interface {
	// EventType returns a stable string identifier used for routing
	// and serialization. Renaming a type is a breaking change to the
	// event log; use upcasters to migrate instead.
	EventType() string
}

// Envelope wraps a domain event with the metadata the framework needs
// to store, route, and replay it. Application code rarely constructs
// envelopes directly — the aggregate repository does it on commit.
type Envelope struct {
	// AggregateID identifies the stream this event belongs to. Must be a
	// stable UUID for the lifetime of the aggregate; UUIDv7 is recommended
	// because its time-ordered prefix gives the events table good B-tree
	// locality.
	AggregateID uuid.UUID

	// AggregateType is the kind of aggregate that produced the event
	// (e.g. "Order"). Useful for routing and replay filtering.
	AggregateType string

	// Sequence is the per-stream version number, starting at 1.
	// Used for optimistic concurrency on append.
	Sequence uint64

	// Timestamp is when the aggregate emitted the event.
	Timestamp time.Time

	// Payload is the actual domain event.
	Payload Event

	// Metadata carries cross-cutting context (correlation IDs, user IDs,
	// trace spans). Keep it small — it's persisted with every event.
	Metadata map[string]string
}

// Handler reacts to a single event envelope. Returning an error signals
// the bus to apply its retry/dead-letter policy.
//
// Handlers should be idempotent. Events may be redelivered on retry
// or replay; a handler that increments a counter without a dedupe key
// will produce wrong projections.
type Handler func(ctx context.Context, env Envelope) error

// Bus delivers events to subscribed handlers. Implementations decide
// whether delivery is synchronous (in-process), asynchronous (channels),
// or networked (Kafka, NATS) — the contract is the same.
type Bus interface {
	// Publish delivers env to every handler that subscribed to its type.
	// Returns the first error encountered, if any.
	Publish(ctx context.Context, env Envelope) error

	// Subscribe registers h to receive events of the given eventType.
	// Multiple handlers per type are allowed; each receives every event.
	Subscribe(eventType string, h Handler)
}

// Store persists event streams. One stream per aggregate ID. Events
// within a stream are totally ordered by Sequence.
type Store interface {
	// Append writes events to the stream identified by aggregateID.
	// expectedVersion is the sequence of the last event the caller
	// observed; if the stream's actual head differs, append fails with
	// ErrConcurrencyConflict. Pass 0 to assert the stream is new.
	Append(ctx context.Context, aggregateID uuid.UUID, expectedVersion uint64, events []Envelope) error

	// Load returns every event in the stream, in order. Used by the
	// aggregate repository to rebuild state.
	Load(ctx context.Context, aggregateID uuid.UUID) ([]Envelope, error)
}
