package postgres_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/dmitrysharkov/goaxon/event"
	"github.com/dmitrysharkov/goaxon/internal/pgtest"
	"github.com/dmitrysharkov/goaxon/store/postgres"
	"github.com/google/uuid"
)

func TestMain(m *testing.M) { pgtest.Run(m) }

// Stable test IDs. Real code should use uuid.NewV7() per aggregate.
var (
	cartID1       = uuid.MustParse("01900000-0000-7000-8000-000000000001")
	cartID2       = uuid.MustParse("01900000-0000-7000-8000-000000000002")
	nonexistentID = uuid.MustParse("01900000-0000-7000-8000-0000000000ff")
)

type itemAdded struct {
	Name string
	Qty  int
}

func (itemAdded) EventType() string { return "ItemAdded" }

type itemRemoved struct {
	Name string
}

func (itemRemoved) EventType() string { return "ItemRemoved" }

func newStore(t *testing.T) *postgres.Store {
	t.Helper()
	pool := pgtest.NewPool(t, postgres.Schema)
	reg := event.NewRegistry()
	event.Register[itemAdded](reg)
	event.Register[itemRemoved](reg)
	return postgres.NewStore(pool, reg)
}

func envelope(aggID uuid.UUID, seq uint64, ev event.Event) event.Envelope {
	return event.Envelope{
		AggregateID:   aggID,
		AggregateType: "Cart",
		Sequence:      seq,
		Timestamp:     time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC),
		Payload:       ev,
	}
}

func TestAppendAndLoad(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	events := []event.Envelope{
		envelope(cartID1, 1, itemAdded{Name: "apple", Qty: 2}),
		envelope(cartID1, 2, itemAdded{Name: "pear", Qty: 1}),
		envelope(cartID1, 3, itemRemoved{Name: "apple"}),
	}
	if err := s.Append(ctx, cartID1, 0, events); err != nil {
		t.Fatalf("append: %v", err)
	}

	got, err := s.Load(ctx, cartID1)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d events, want 3", len(got))
	}
	first, ok := got[0].Payload.(itemAdded)
	if !ok || first.Name != "apple" || first.Qty != 2 {
		t.Fatalf("event 0: got %+v", got[0].Payload)
	}
	if got[2].Sequence != 3 || got[2].AggregateType != "Cart" {
		t.Fatalf("event 2 envelope wrong: %+v", got[2])
	}
	if !got[0].Timestamp.Equal(events[0].Timestamp) {
		t.Fatalf("timestamp mismatch: got %v want %v", got[0].Timestamp, events[0].Timestamp)
	}
}

func TestAppendIncremental(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	if err := s.Append(ctx, cartID1, 0, []event.Envelope{envelope(cartID1, 1, itemAdded{Name: "a", Qty: 1})}); err != nil {
		t.Fatal(err)
	}
	if err := s.Append(ctx, cartID1, 1, []event.Envelope{envelope(cartID1, 2, itemAdded{Name: "b", Qty: 1})}); err != nil {
		t.Fatal(err)
	}

	got, err := s.Load(ctx, cartID1)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[1].Sequence != 2 {
		t.Fatalf("got %+v", got)
	}
}

func TestConcurrencyConflictStaleExpectedVersion(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	if err := s.Append(ctx, cartID1, 0, []event.Envelope{envelope(cartID1, 1, itemAdded{Name: "a", Qty: 1})}); err != nil {
		t.Fatal(err)
	}
	// Caller thinks stream is empty but it isn't.
	err := s.Append(ctx, cartID1, 0, []event.Envelope{envelope(cartID1, 1, itemAdded{Name: "b", Qty: 1})})
	if !errors.Is(err, event.ErrConcurrencyConflict) {
		t.Fatalf("got %v, want ErrConcurrencyConflict", err)
	}
}

func TestConcurrencyConflictUniqueViolation(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	// Two batches racing to write sequence 1. The head check passes for both,
	// but the second insert hits the (aggregate_id, sequence) PK.
	// We simulate by appending the same sequence twice in the second call,
	// after the first has committed.
	if err := s.Append(ctx, cartID1, 0, []event.Envelope{envelope(cartID1, 1, itemAdded{Name: "a", Qty: 1})}); err != nil {
		t.Fatal(err)
	}
	// Caller reports correct expectedVersion=1 but the new event collides on seq 1
	// (e.g. caller built envelopes from a stale snapshot).
	err := s.Append(ctx, cartID1, 1, []event.Envelope{envelope(cartID1, 1, itemAdded{Name: "b", Qty: 1})})
	if !errors.Is(err, event.ErrConcurrencyConflict) {
		t.Fatalf("got %v, want ErrConcurrencyConflict", err)
	}
}

func TestLoadEmptyStream(t *testing.T) {
	s := newStore(t)
	got, err := s.Load(context.Background(), nonexistentID)
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatalf("got %+v, want nil", got)
	}
}

func TestStreamsAreIsolated(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	if err := s.Append(ctx, cartID1, 0, []event.Envelope{envelope(cartID1, 1, itemAdded{Name: "a", Qty: 1})}); err != nil {
		t.Fatal(err)
	}
	if err := s.Append(ctx, cartID2, 0, []event.Envelope{envelope(cartID2, 1, itemAdded{Name: "b", Qty: 2})}); err != nil {
		t.Fatal(err)
	}

	got1, _ := s.Load(ctx, cartID1)
	got2, _ := s.Load(ctx, cartID2)
	if len(got1) != 1 || len(got2) != 1 {
		t.Fatalf("got %d / %d", len(got1), len(got2))
	}
	if got1[0].Payload.(itemAdded).Name != "a" {
		t.Fatalf("cart-1 wrong: %+v", got1[0].Payload)
	}
	if got2[0].Payload.(itemAdded).Name != "b" {
		t.Fatalf("cart-2 wrong: %+v", got2[0].Payload)
	}
}

func TestMetadataRoundTrip(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	env := envelope(cartID1, 1, itemAdded{Name: "a", Qty: 1})
	env.Metadata = map[string]string{"correlation_id": "abc-123", "user_id": "u-7"}
	if err := s.Append(ctx, cartID1, 0, []event.Envelope{env}); err != nil {
		t.Fatal(err)
	}

	got, err := s.Load(ctx, cartID1)
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Metadata["correlation_id"] != "abc-123" || got[0].Metadata["user_id"] != "u-7" {
		t.Fatalf("metadata round trip: %+v", got[0].Metadata)
	}
}

func TestAppendEmptyIsNoop(t *testing.T) {
	s := newStore(t)
	if err := s.Append(context.Background(), cartID1, 0, nil); err != nil {
		t.Fatalf("empty append: %v", err)
	}
}

// --- Outbox tests ---

func TestAppendWritesOutboxRows(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	events := []event.Envelope{
		envelope(cartID1, 1, itemAdded{Name: "a", Qty: 1}),
		envelope(cartID1, 2, itemAdded{Name: "b", Qty: 2}),
	}
	if err := s.Append(ctx, cartID1, 0, events); err != nil {
		t.Fatal(err)
	}

	pending, err := s.LoadPending(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 2 {
		t.Fatalf("got %d pending, want 2", len(pending))
	}
	if pending[0].Envelope.Payload.(itemAdded).Name != "a" {
		t.Fatalf("entry 0: %+v", pending[0])
	}
	if pending[0].OutboxID >= pending[1].OutboxID {
		t.Fatalf("expected ascending outbox IDs, got %d, %d", pending[0].OutboxID, pending[1].OutboxID)
	}
}

func TestMarkDispatchedRemovesFromPending(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	if err := s.Append(ctx, cartID1, 0, []event.Envelope{
		envelope(cartID1, 1, itemAdded{Name: "a", Qty: 1}),
		envelope(cartID1, 2, itemAdded{Name: "b", Qty: 2}),
	}); err != nil {
		t.Fatal(err)
	}

	pending, _ := s.LoadPending(ctx, 10)
	ids := []int64{pending[0].OutboxID}
	if err := s.MarkDispatched(ctx, ids); err != nil {
		t.Fatal(err)
	}

	stillPending, _ := s.LoadPending(ctx, 10)
	if len(stillPending) != 1 {
		t.Fatalf("got %d pending after mark, want 1", len(stillPending))
	}
	if stillPending[0].OutboxID != pending[1].OutboxID {
		t.Fatalf("wrong entry remained: %+v", stillPending[0])
	}
}

func TestLoadPendingRespectsBatchSize(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	for i := 1; i <= 5; i++ {
		if err := s.Append(ctx, cartID1, uint64(i-1), []event.Envelope{
			envelope(cartID1, uint64(i), itemAdded{Name: "x", Qty: i}),
		}); err != nil {
			t.Fatal(err)
		}
	}

	pending, err := s.LoadPending(ctx, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 3 {
		t.Fatalf("got %d, want 3", len(pending))
	}
}

func TestMarkDispatchedEmptyIsNoop(t *testing.T) {
	s := newStore(t)
	if err := s.MarkDispatched(context.Background(), nil); err != nil {
		t.Fatalf("empty mark: %v", err)
	}
}

// TestAppendAtomicityOnFailure verifies that if any single insert in a
// batch fails, NO outbox rows leak. We trigger failure by giving the
// second envelope a duplicate sequence — the first event row inserts,
// then the unique constraint fires on the second, rolling back the tx.
func TestAppendAtomicityOnFailure(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	// First append succeeds.
	if err := s.Append(ctx, cartID1, 0, []event.Envelope{
		envelope(cartID1, 1, itemAdded{Name: "a", Qty: 1}),
	}); err != nil {
		t.Fatal(err)
	}
	dispatched, _ := s.LoadPending(ctx, 10)
	for _, e := range dispatched {
		_ = s.MarkDispatched(ctx, []int64{e.OutboxID})
	}

	// Second append: claims expectedVersion=1 (correct) but reuses sequence 1.
	// First event in the batch will conflict with existing row.
	err := s.Append(ctx, cartID1, 1, []event.Envelope{
		envelope(cartID1, 1, itemAdded{Name: "b", Qty: 1}),
	})
	if !errors.Is(err, event.ErrConcurrencyConflict) {
		t.Fatalf("got %v, want ErrConcurrencyConflict", err)
	}

	// Outbox must not contain a "b" row from the failed tx.
	pending, _ := s.LoadPending(ctx, 10)
	if len(pending) != 0 {
		t.Fatalf("found leaked outbox rows after failed append: %+v", pending)
	}
}
