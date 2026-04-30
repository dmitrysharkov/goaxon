package event_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/dmitrysharkov/goaxon/event"
	"github.com/google/uuid"
)

var stubAggregateID = uuid.MustParse("01900000-0000-7000-8000-000000000a01")

// --- stubs ---

type stubOutbox struct {
	mu      sync.Mutex
	entries []event.OutboxEntry
	nextID  int64
	loadErr error
	markErr error
}

func (s *stubOutbox) add(payload event.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextID++
	s.entries = append(s.entries, event.OutboxEntry{
		OutboxID: s.nextID,
		Envelope: event.Envelope{
			AggregateID:   stubAggregateID,
			AggregateType: "Agg",
			Sequence:      uint64(s.nextID),
			Payload:       payload,
		},
	})
}

// Claim implements event.Outbox. The stub doesn't model row-level
// locking — Dispatcher unit tests don't exercise multi-claim concurrency
// (that's an integration concern, covered by the postgres tests).
func (s *stubOutbox) Claim(_ context.Context, batchSize int) (event.Claim, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.loadErr != nil {
		return nil, s.loadErr
	}
	if batchSize > len(s.entries) {
		batchSize = len(s.entries)
	}
	return &stubClaim{
		parent:  s,
		entries: append([]event.OutboxEntry(nil), s.entries[:batchSize]...),
	}, nil
}

func (s *stubOutbox) pending() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.entries)
}

type stubClaim struct {
	parent  *stubOutbox
	entries []event.OutboxEntry
}

func (c *stubClaim) Entries() []event.OutboxEntry { return c.entries }

func (c *stubClaim) Commit(_ context.Context, ids []int64) error {
	c.parent.mu.Lock()
	defer c.parent.mu.Unlock()
	if c.parent.markErr != nil {
		return c.parent.markErr
	}
	if len(ids) == 0 {
		return nil
	}
	mark := make(map[int64]bool, len(ids))
	for _, id := range ids {
		mark[id] = true
	}
	kept := c.parent.entries[:0]
	for _, e := range c.parent.entries {
		if !mark[e.OutboxID] {
			kept = append(kept, e)
		}
	}
	c.parent.entries = kept
	return nil
}

func (c *stubClaim) Release(_ context.Context) error { return nil }

type stubBus struct {
	mu       sync.Mutex
	got      []event.Envelope
	failOn   string
	failOnce bool
	failed   bool
}

func (b *stubBus) Publish(_ context.Context, env event.Envelope) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.failOn != "" && env.Payload.EventType() == b.failOn {
		if !b.failOnce || !b.failed {
			b.failed = true
			return errors.New("simulated publish failure")
		}
	}
	b.got = append(b.got, env)
	return nil
}

func (b *stubBus) Subscribe(string, event.Handler) {}

func (b *stubBus) published() []event.Envelope {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]event.Envelope(nil), b.got...)
}

// waitFor polls predicate every 5ms until it returns true or 2s elapses.
func waitFor(t *testing.T, predicate func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if predicate() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for: %s", msg)
}

// --- tests ---

func TestDispatcherDrainsPending(t *testing.T) {
	out := &stubOutbox{}
	bus := &stubBus{}
	out.add(itemAdded{Name: "a"})
	out.add(itemAdded{Name: "b"})

	disp := event.NewDispatcher(out, bus, event.WithPollInterval(5*time.Millisecond))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- disp.Run(ctx) }()

	waitFor(t, func() bool { return len(bus.published()) == 2 }, "both events published")
	waitFor(t, func() bool { return out.pending() == 0 }, "outbox drained")

	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("Run returned %v, want context.Canceled", err)
	}
}

func TestDispatcherStopsBatchOnPublishError(t *testing.T) {
	out := &stubOutbox{}
	bus := &stubBus{failOn: "ItemRemoved", failOnce: true}
	out.add(itemAdded{Name: "a"})    // OutboxID 1, succeeds
	out.add(itemRemoved{Name: "x"})  // OutboxID 2, fails first time
	out.add(itemAdded{Name: "b"})    // OutboxID 3, must NOT publish before #2

	var errs []error
	var errsMu sync.Mutex
	disp := event.NewDispatcher(out, bus,
		event.WithPollInterval(5*time.Millisecond),
		event.WithErrorHandler(func(_ context.Context, err error) {
			errsMu.Lock()
			errs = append(errs, err)
			errsMu.Unlock()
		}),
	)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- disp.Run(ctx) }()

	// Eventually all three publish (after retry of #2 succeeds).
	waitFor(t, func() bool { return len(bus.published()) == 3 }, "all three eventually published")
	waitFor(t, func() bool { return out.pending() == 0 }, "outbox drained")

	// Order on the bus must be a, x, b — the failed first attempt at x
	// did NOT let b through.
	got := bus.published()
	if got[0].Payload.(itemAdded).Name != "a" ||
		got[1].Payload.(itemRemoved).Name != "x" ||
		got[2].Payload.(itemAdded).Name != "b" {
		t.Fatalf("ordering violated: %+v", got)
	}

	cancel()
	<-done

	errsMu.Lock()
	defer errsMu.Unlock()
	if len(errs) == 0 {
		t.Fatal("expected at least one error reported via callback")
	}
}

func TestDispatcherRespectsContextCancel(t *testing.T) {
	out := &stubOutbox{}
	bus := &stubBus{}
	disp := event.NewDispatcher(out, bus, event.WithPollInterval(50*time.Millisecond))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- disp.Run(ctx) }()

	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("got %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run did not return after cancel")
	}
}

func TestDispatcherSurfacesLoadErrors(t *testing.T) {
	out := &stubOutbox{loadErr: errors.New("db down")}
	bus := &stubBus{}

	var got error
	var gotMu sync.Mutex
	disp := event.NewDispatcher(out, bus,
		event.WithPollInterval(5*time.Millisecond),
		event.WithErrorHandler(func(_ context.Context, err error) {
			gotMu.Lock()
			defer gotMu.Unlock()
			if got == nil {
				got = err
			}
		}),
	)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go disp.Run(ctx)

	waitFor(t, func() bool {
		gotMu.Lock()
		defer gotMu.Unlock()
		return got != nil
	}, "load error reported")

	gotMu.Lock()
	defer gotMu.Unlock()
	if got == nil || !errors.Is(got, out.loadErr) {
		t.Fatalf("got %v, want wrapped 'db down'", got)
	}
}
