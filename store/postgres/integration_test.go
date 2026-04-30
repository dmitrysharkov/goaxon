package postgres_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/dmitrysharkov/goaxon/aggregate"
	"github.com/dmitrysharkov/goaxon/event"
	"github.com/dmitrysharkov/goaxon/internal/pgtest"
	"github.com/dmitrysharkov/goaxon/store/memory"
	"github.com/dmitrysharkov/goaxon/store/postgres"
	"github.com/google/uuid"
)

// --- minimal aggregate for the integration test ---

type cart struct {
	*aggregate.Base
	items []string
}

func (cart) AggregateType() string { return "Cart" }

func (c *cart) Apply(e event.Event) {
	if ev, ok := e.(itemAdded); ok {
		c.items = append(c.items, ev.Name)
	}
}

func (c *cart) Add(name string, qty int) {
	c.Raise(c, itemAdded{Name: name, Qty: qty})
}

func newCart(id uuid.UUID) *cart {
	c := &cart{Base: &aggregate.Base{}}
	_ = c.SetID(id)
	return c
}

// --- helper ---

func waitFor(t *testing.T, predicate func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if predicate() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for: %s", msg)
}

// --- end-to-end test ---

// TestIntegrationRepositoryDispatcherFlow exercises the full path:
// command logic → Repository.Save (writes events + outbox in one tx) →
// Dispatcher polls outbox → publishes to memory bus → handler receives.
func TestIntegrationRepositoryDispatcherFlow(t *testing.T) {
	pool := pgtest.NewPool(t, postgres.Schema)
	reg := event.NewRegistry()
	event.Register[itemAdded](reg)

	store := postgres.NewStore(pool, reg)
	bus := memory.NewBus()

	var (
		muRecv   sync.Mutex
		received []event.Envelope
	)
	bus.Subscribe("ItemAdded", func(_ context.Context, env event.Envelope) error {
		muRecv.Lock()
		defer muRecv.Unlock()
		received = append(received, env)
		return nil
	})

	repo := aggregate.NewRepository[*cart](store, bus, newCart)
	c := newCart(cartID1)
	c.Add("apple", 2)
	c.Add("pear", 1)

	if err := repo.Save(context.Background(), c); err != nil {
		t.Fatalf("save: %v", err)
	}

	// Save with an Outbox-backed store must NOT publish synchronously.
	muRecv.Lock()
	if len(received) != 0 {
		muRecv.Unlock()
		t.Fatalf("bus received %d events before dispatcher ran", len(received))
	}
	muRecv.Unlock()

	// Outbox should hold both pending entries.
	pending, err := store.LoadPending(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 2 {
		t.Fatalf("outbox pending = %d, want 2", len(pending))
	}

	// Start the dispatcher.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	disp := event.NewDispatcher(store, bus, event.WithPollInterval(10*time.Millisecond))
	done := make(chan error, 1)
	go func() { done <- disp.Run(ctx) }()

	waitFor(t, func() bool {
		muRecv.Lock()
		defer muRecv.Unlock()
		return len(received) == 2
	}, "both events delivered via dispatcher")

	waitFor(t, func() bool {
		p, _ := store.LoadPending(context.Background(), 10)
		return len(p) == 0
	}, "outbox marked dispatched")

	muRecv.Lock()
	defer muRecv.Unlock()
	if received[0].Sequence != 1 || received[1].Sequence != 2 {
		t.Fatalf("ordering wrong: got seq %d, %d", received[0].Sequence, received[1].Sequence)
	}
	if received[0].Payload.(itemAdded).Name != "apple" {
		t.Fatalf("first event payload wrong: %+v", received[0].Payload)
	}

	cancel()
	<-done
}

// TestIntegrationLoadAfterRestart exercises replay through Repository.Load:
// after the events are saved, a fresh Repository can rebuild aggregate
// state from the event stream (the dispatcher path is irrelevant here —
// Load reads from the events table, not the outbox).
func TestIntegrationLoadAfterRestart(t *testing.T) {
	pool := pgtest.NewPool(t, postgres.Schema)
	reg := event.NewRegistry()
	event.Register[itemAdded](reg)

	store := postgres.NewStore(pool, reg)
	bus := memory.NewBus()
	repo := aggregate.NewRepository[*cart](store, bus, newCart)

	c := newCart(cartID1)
	c.Add("apple", 2)
	c.Add("pear", 1)
	if err := repo.Save(context.Background(), c); err != nil {
		t.Fatal(err)
	}

	// Simulate a restart by building a fresh repository on the same store.
	repo2 := aggregate.NewRepository[*cart](store, bus, newCart)
	loaded, err := repo2.Load(context.Background(), cartID1)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.items) != 2 || loaded.items[0] != "apple" || loaded.items[1] != "pear" {
		t.Fatalf("replay produced wrong state: %+v", loaded.items)
	}
	if loaded.Version() != 2 {
		t.Fatalf("version: got %d, want 2", loaded.Version())
	}
}
