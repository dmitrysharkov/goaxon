package aggregate_test

import (
	"context"
	"sync"
	"testing"

	"github.com/dmitrysharkov/goaxon/aggregate"
	"github.com/dmitrysharkov/goaxon/event"
	"github.com/google/uuid"
)

var cartID = uuid.MustParse("01900000-0000-7000-8000-00000000c1c1")

// --- test fixtures ---

type itemAdded struct{ Name string }

func (itemAdded) EventType() string { return "ItemAdded" }

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

func (c *cart) Add(name string) {
	c.Raise(c, itemAdded{Name: name})
}

func newCart(id uuid.UUID) *cart {
	c := &cart{Base: &aggregate.Base{}}
	_ = c.SetID(id)
	return c
}

// --- recording stubs ---

type recordingBus struct {
	mu       sync.Mutex
	publish  []event.Envelope
	handlers map[string][]event.Handler
}

func newRecordingBus() *recordingBus {
	return &recordingBus{handlers: make(map[string][]event.Handler)}
}

func (b *recordingBus) Publish(_ context.Context, env event.Envelope) error {
	b.mu.Lock()
	b.publish = append(b.publish, env)
	b.mu.Unlock()
	return nil
}

func (b *recordingBus) Subscribe(eventType string, h event.Handler) {
	b.handlers[eventType] = append(b.handlers[eventType], h)
}

type plainStore struct {
	mu      sync.Mutex
	streams map[uuid.UUID][]event.Envelope
}

func newPlainStore() *plainStore {
	return &plainStore{streams: make(map[uuid.UUID][]event.Envelope)}
}

func (s *plainStore) Append(_ context.Context, id uuid.UUID, expected uint64, evs []event.Envelope) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if uint64(len(s.streams[id])) != expected {
		return event.ErrConcurrencyConflict
	}
	s.streams[id] = append(s.streams[id], evs...)
	return nil
}

func (s *plainStore) Load(_ context.Context, id uuid.UUID) ([]event.Envelope, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]event.Envelope(nil), s.streams[id]...), nil
}

// outboxStore satisfies both event.Store and event.Outbox; it's a
// plainStore plus a no-op Claim — the test only cares that the
// Repository sees the Outbox interface and skips synchronous publish.
type outboxStore struct {
	*plainStore
}

func newOutboxStore() *outboxStore { return &outboxStore{plainStore: newPlainStore()} }

func (*outboxStore) Claim(context.Context, int) (event.Claim, error) {
	return emptyClaim{}, nil
}

type emptyClaim struct{}

func (emptyClaim) Entries() []event.OutboxEntry             { return nil }
func (emptyClaim) Commit(context.Context, []int64) error    { return nil }
func (emptyClaim) Release(context.Context) error            { return nil }

// --- tests ---

func TestSavePublishesWhenStoreHasNoOutbox(t *testing.T) {
	store := newPlainStore()
	bus := newRecordingBus()
	repo := aggregate.NewRepository[*cart](store, bus, newCart)

	c := newCart(cartID)
	c.Add("apple")
	c.Add("pear")

	if err := repo.Save(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	if len(bus.publish) != 2 {
		t.Fatalf("got %d publishes, want 2", len(bus.publish))
	}
	if bus.publish[0].Sequence != 1 || bus.publish[1].Sequence != 2 {
		t.Fatalf("seq wrong: %d, %d", bus.publish[0].Sequence, bus.publish[1].Sequence)
	}
}

func TestSaveSkipsPublishWhenStoreHasOutbox(t *testing.T) {
	store := newOutboxStore()
	bus := newRecordingBus()
	repo := aggregate.NewRepository[*cart](store, bus, newCart)

	c := newCart(cartID)
	c.Add("apple")

	if err := repo.Save(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	if len(bus.publish) != 0 {
		t.Fatalf("got %d publishes, want 0 (outbox should handle dispatch)", len(bus.publish))
	}
	// Events must still be persisted.
	stream, _ := store.Load(context.Background(), cartID)
	if len(stream) != 1 {
		t.Fatalf("got %d stored, want 1", len(stream))
	}
}

func TestLoadReplaysEvents(t *testing.T) {
	store := newPlainStore()
	bus := newRecordingBus()
	repo := aggregate.NewRepository[*cart](store, bus, newCart)

	c := newCart(cartID)
	c.Add("apple")
	c.Add("pear")
	if err := repo.Save(context.Background(), c); err != nil {
		t.Fatal(err)
	}

	loaded, err := repo.Load(context.Background(), cartID)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.items) != 2 || loaded.items[0] != "apple" || loaded.items[1] != "pear" {
		t.Fatalf("replay wrong: %+v", loaded.items)
	}
	if loaded.Version() != 2 {
		t.Fatalf("version: got %d want 2", loaded.Version())
	}
}
