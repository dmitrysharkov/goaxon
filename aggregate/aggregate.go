// Package aggregate defines the building blocks for event-sourced
// aggregate roots and the repository that loads and persists them.
package aggregate

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/dmitrysharkov/goaxon/event"
	"github.com/google/uuid"
)

// Root is the interface every aggregate must implement.
//
// Aggregates have two responsibilities:
//   - Apply events to mutate their internal state (deterministic, no
//     side effects). Apply is called both during replay and after a
//     command handler emits a new event.
//   - Validate commands and emit events. That logic lives in command
//     handler methods on the concrete aggregate type, not in this
//     interface — different aggregates handle different commands.
type Root interface {
	// AggregateID returns the stream identifier. Must be stable for
	// the lifetime of the aggregate. UUIDv7 is recommended (time-ordered).
	AggregateID() uuid.UUID

	// AggregateType returns a stable type name (e.g. "Order").
	AggregateType() string

	// Apply mutates the aggregate's state in response to an event.
	// Must be deterministic and free of side effects — it runs during
	// replay too.
	Apply(e event.Event)
}

// Base is an embeddable struct that gives an aggregate the bookkeeping
// it needs to track uncommitted events and the current version. Embed
// it in your concrete aggregate types.
//
//	type Order struct {
//	    aggregate.Base
//	    Status OrderStatus
//	    // ...
//	}
type Base struct {
	id          uuid.UUID
	version     uint64
	uncommitted []event.Event
}

// SetID assigns the aggregate's stream identifier. Call this from
// the constructor of new aggregates. It returns an error if called
// twice — the ID is immutable once set — or if id is the zero UUID.
func (b *Base) SetID(id uuid.UUID) error {
	if b.id != uuid.Nil {
		return errors.New("aggregate: ID already set")
	}
	if id == uuid.Nil {
		return errors.New("aggregate: ID cannot be the zero UUID")
	}
	b.id = id
	return nil
}

// AggregateID returns the stream identifier.
func (b *Base) AggregateID() uuid.UUID { return b.id }

// Version returns the sequence number of the last applied event.
// Zero means no events have been applied yet.
func (b *Base) Version() uint64 { return b.version }

// Raise records a new event, applies it to the aggregate (so subsequent
// command handlers see the updated state), and queues it for persistence.
// Concrete aggregate types call this from their command handler methods.
//
// The caller must pass the aggregate itself as `self` so Raise can
// dispatch Apply on the concrete type — Go has no `this` pointer in
// embedded structs.
func (b *Base) Raise(self Root, e event.Event) {
	self.Apply(e)
	b.uncommitted = append(b.uncommitted, e)
	b.version++
}

// Uncommitted returns events emitted since the last load or commit.
// The repository drains this list on save.
func (b *Base) Uncommitted() []event.Event { return b.uncommitted }

// markCommitted clears uncommitted events. Called by the repository
// after a successful append.
func (b *Base) markCommitted() { b.uncommitted = nil }

// rehydrate replays a stored event during loading without queuing it
// for re-persistence. The repository uses this; user code should not.
func (b *Base) rehydrate(self Root, e event.Event) {
	self.Apply(e)
	b.version++
}

// Repository loads and saves aggregates by replaying their event streams.
// It's parameterized over the concrete aggregate type so callers don't
// have to type-assert on every load.
type Repository[A Root] struct {
	store   event.Store
	bus     event.Bus
	factory func(id uuid.UUID) A
}

// NewRepository wires a repository for aggregate type A.
//
// factory is a constructor that returns a zero-value aggregate with its
// ID set. The repository calls it before replaying events, so factories
// should not perform any business logic — just allocate and tag.
func NewRepository[A Root](store event.Store, bus event.Bus, factory func(id uuid.UUID) A) *Repository[A] {
	return &Repository[A]{store: store, bus: bus, factory: factory}
}

// baseAccessor is implemented by aggregates that embed *Base. The
// repository uses it to drive lifecycle methods without exposing them
// in the public Root interface.
type baseAccessor interface {
	Root
	getBase() *Base
}

// getBase exposes the embedded Base to the repository. Aggregates
// embedding Base get this method automatically through promotion if
// they define it; the helper below makes it explicit.
func (b *Base) getBase() *Base { return b }

// Load reconstructs an aggregate by replaying its event stream.
func (r *Repository[A]) Load(ctx context.Context, id uuid.UUID) (A, error) {
	var zero A
	envelopes, err := r.store.Load(ctx, id)
	if err != nil {
		return zero, fmt.Errorf("aggregate: load %s: %w", id, err)
	}
	agg := r.factory(id)
	ba, ok := any(agg).(baseAccessor)
	if !ok {
		return zero, fmt.Errorf("aggregate: %T must embed *aggregate.Base", agg)
	}
	for _, env := range envelopes {
		ba.getBase().rehydrate(agg, env.Payload)
	}
	return agg, nil
}

// Save persists uncommitted events and publishes them to the bus.
//
// Two delivery modes, picked at runtime by type-asserting r.store
// against event.Outbox:
//
//   - Plain Store (e.g. in-memory): append, then synchronously
//     bus.Publish each event. Append-and-publish is NOT atomic; a
//     crash between the two leaves events persisted but unpublished.
//     Acceptable for in-process setups; not for durable stores.
//
//   - Outbox-capable Store (e.g. Postgres): append writes events +
//     outbox rows in a single transaction. Save then returns; an
//     out-of-band dispatcher reads the outbox and publishes. This
//     is the crash-safe path.
func (r *Repository[A]) Save(ctx context.Context, agg A) error {
	ba, ok := any(agg).(baseAccessor)
	if !ok {
		return fmt.Errorf("aggregate: %T must embed *aggregate.Base", agg)
	}
	base := ba.getBase()
	pending := base.Uncommitted()
	if len(pending) == 0 {
		return nil
	}

	expectedVersion := base.Version() - uint64(len(pending))
	envelopes := make([]event.Envelope, len(pending))
	for i, e := range pending {
		envelopes[i] = event.Envelope{
			AggregateID:   agg.AggregateID(),
			AggregateType: agg.AggregateType(),
			Sequence:      expectedVersion + uint64(i) + 1,
			Timestamp:     time.Now().UTC(),
			Payload:       e,
		}
	}

	if err := r.store.Append(ctx, agg.AggregateID(), expectedVersion, envelopes); err != nil {
		return fmt.Errorf("aggregate: append: %w", err)
	}
	base.markCommitted()

	if _, hasOutbox := r.store.(event.Outbox); hasOutbox {
		return nil
	}
	for _, env := range envelopes {
		if err := r.bus.Publish(ctx, env); err != nil {
			return fmt.Errorf("aggregate: publish %s: %w", env.Payload.EventType(), err)
		}
	}
	return nil
}
