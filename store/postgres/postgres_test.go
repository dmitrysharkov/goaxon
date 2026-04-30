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

func TestLoadEmptyStreamReturnsNotFound(t *testing.T) {
	s := newStore(t)
	got, err := s.Load(context.Background(), nonexistentID)
	if !errors.Is(err, event.ErrStreamNotFound) {
		t.Fatalf("err = %v, want event.ErrStreamNotFound", err)
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

// peekPending opens a Claim, reads its entries, and immediately
// Releases. Used by tests as a non-mutating "what's currently pending?"
// inspection (the production path is Claim → publish → Commit).
func peekPending(t *testing.T, s *postgres.Store, batchSize int) []event.OutboxEntry {
	t.Helper()
	c, err := s.Claim(context.Background(), batchSize)
	if err != nil {
		t.Fatalf("peek claim: %v", err)
	}
	entries := append([]event.OutboxEntry(nil), c.Entries()...)
	if err := c.Release(context.Background()); err != nil {
		t.Fatalf("peek release: %v", err)
	}
	return entries
}

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

	pending := peekPending(t, s, 10)
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

func TestClaimCommitMarksDispatched(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	if err := s.Append(ctx, cartID1, 0, []event.Envelope{
		envelope(cartID1, 1, itemAdded{Name: "a", Qty: 1}),
		envelope(cartID1, 2, itemAdded{Name: "b", Qty: 2}),
	}); err != nil {
		t.Fatal(err)
	}

	c, err := s.Claim(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	entries := c.Entries()
	if len(entries) != 2 {
		t.Fatalf("got %d, want 2", len(entries))
	}
	// Commit only the first; second stays pending.
	if err := c.Commit(ctx, []int64{entries[0].OutboxID}); err != nil {
		t.Fatal(err)
	}

	stillPending := peekPending(t, s, 10)
	if len(stillPending) != 1 {
		t.Fatalf("got %d pending after commit, want 1", len(stillPending))
	}
	if stillPending[0].OutboxID != entries[1].OutboxID {
		t.Fatalf("wrong entry remained: %+v", stillPending[0])
	}
}

func TestClaimReleaseDoesNotMark(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	if err := s.Append(ctx, cartID1, 0, []event.Envelope{
		envelope(cartID1, 1, itemAdded{Name: "a", Qty: 1}),
	}); err != nil {
		t.Fatal(err)
	}

	c, err := s.Claim(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Entries()) != 1 {
		t.Fatal("expected 1 entry")
	}
	if err := c.Release(ctx); err != nil {
		t.Fatal(err)
	}

	// After release, the entry must still be claimable.
	pending := peekPending(t, s, 10)
	if len(pending) != 1 {
		t.Fatalf("got %d pending after release, want 1", len(pending))
	}
}

func TestClaimRespectsBatchSize(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	for i := 1; i <= 5; i++ {
		if err := s.Append(ctx, cartID1, uint64(i-1), []event.Envelope{
			envelope(cartID1, uint64(i), itemAdded{Name: "x", Qty: i}),
		}); err != nil {
			t.Fatal(err)
		}
	}

	c, err := s.Claim(ctx, 3)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Release(ctx)
	if len(c.Entries()) != 3 {
		t.Fatalf("got %d, want 3", len(c.Entries()))
	}
}

func TestClaimEmptyOutboxIsNoop(t *testing.T) {
	s := newStore(t)
	c, err := s.Claim(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Entries()) != 0 {
		t.Fatalf("got %d, want 0", len(c.Entries()))
	}
	// Both Commit and Release on an empty claim must succeed.
	if err := c.Commit(context.Background(), nil); err != nil {
		t.Fatalf("commit empty: %v", err)
	}
	if err := c.Release(context.Background()); err != nil {
		t.Fatalf("release empty: %v", err)
	}
}

// TestClaimSkipsLockedRows is the headline test for SKIP LOCKED:
// two simultaneous Claims must see disjoint sets of entries.
func TestClaimSkipsLockedRows(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	for i := 1; i <= 5; i++ {
		if err := s.Append(ctx, cartID1, uint64(i-1), []event.Envelope{
			envelope(cartID1, uint64(i), itemAdded{Name: "x", Qty: i}),
		}); err != nil {
			t.Fatal(err)
		}
	}

	first, err := s.Claim(ctx, 3)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Release(ctx)
	if len(first.Entries()) != 3 {
		t.Fatalf("first claim got %d, want 3", len(first.Entries()))
	}

	// Second claim, while the first is still open, must skip the
	// 3 locked rows and return only the remaining 2.
	second, err := s.Claim(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Release(ctx)
	if len(second.Entries()) != 2 {
		t.Fatalf("second claim got %d, want 2 (locked rows should be skipped)", len(second.Entries()))
	}

	seen := map[int64]bool{}
	for _, e := range first.Entries() {
		seen[e.OutboxID] = true
	}
	for _, e := range second.Entries() {
		if seen[e.OutboxID] {
			t.Fatalf("second claim saw locked entry %d", e.OutboxID)
		}
	}
}

// TestReleaseMakesRowsClaimableAgain proves the lock lifetime is bounded
// by Commit/Release and not by the Postgres connection or Claim handle.
func TestReleaseMakesRowsClaimableAgain(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	for i := 1; i <= 3; i++ {
		if err := s.Append(ctx, cartID1, uint64(i-1), []event.Envelope{
			envelope(cartID1, uint64(i), itemAdded{Name: "x", Qty: i}),
		}); err != nil {
			t.Fatal(err)
		}
	}

	first, err := s.Claim(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Entries()) != 3 {
		t.Fatalf("first got %d, want 3", len(first.Entries()))
	}
	if err := first.Release(ctx); err != nil {
		t.Fatal(err)
	}

	second, err := s.Claim(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Release(ctx)
	if len(second.Entries()) != 3 {
		t.Fatalf("after release, second claim got %d, want 3", len(second.Entries()))
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
	// Drain the outbox so the next failed append's leak (if any) shows up cleanly.
	c, err := s.Claim(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]int64, 0, len(c.Entries()))
	for _, e := range c.Entries() {
		ids = append(ids, e.OutboxID)
	}
	if err := c.Commit(ctx, ids); err != nil {
		t.Fatal(err)
	}

	// Second append: claims expectedVersion=1 (correct) but reuses sequence 1.
	// First event in the batch will conflict with existing row.
	err = s.Append(ctx, cartID1, 1, []event.Envelope{
		envelope(cartID1, 1, itemAdded{Name: "b", Qty: 1}),
	})
	if !errors.Is(err, event.ErrConcurrencyConflict) {
		t.Fatalf("got %v, want ErrConcurrencyConflict", err)
	}

	// Outbox must not contain a "b" row from the failed tx.
	pending := peekPending(t, s, 10)
	if len(pending) != 0 {
		t.Fatalf("found leaked outbox rows after failed append: %+v", pending)
	}
}
