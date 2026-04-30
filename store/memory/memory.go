// Package memory provides in-memory implementations of the event Store
// and Bus interfaces. They're suitable for tests, examples, and small
// single-process applications. For anything else, use a persistent
// store (postgres, eventstoredb).
package memory

import (
	"context"
	"sync"

	"github.com/dmitrysharkov/goaxon/event"
)

// Store is an in-memory append-only event store. Streams are keyed by
// aggregate ID. Safe for concurrent use.
type Store struct {
	mu      sync.RWMutex
	streams map[string][]event.Envelope
}

// NewStore returns an empty in-memory store.
func NewStore() *Store {
	return &Store{streams: make(map[string][]event.Envelope)}
}

// Append implements event.Store. It enforces optimistic concurrency by
// comparing expectedVersion against the current head sequence.
func (s *Store) Append(ctx context.Context, aggregateID string, expectedVersion uint64, events []event.Envelope) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	current := uint64(len(s.streams[aggregateID]))
	if current != expectedVersion {
		return event.ErrConcurrencyConflict
	}
	s.streams[aggregateID] = append(s.streams[aggregateID], events...)
	return nil
}

// Load implements event.Store.
func (s *Store) Load(ctx context.Context, aggregateID string) ([]event.Envelope, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	stream, ok := s.streams[aggregateID]
	if !ok {
		return nil, nil // empty stream is fine; caller starts from a fresh aggregate
	}
	// Return a copy so callers can't mutate our internal state.
	out := make([]event.Envelope, len(stream))
	copy(out, stream)
	return out, nil
}

// Bus is an in-memory event bus that delivers events synchronously to
// every subscribed handler. Errors from handlers are collected; the
// first non-nil error is returned and remaining handlers still run.
//
// "Synchronous" makes failures easy to test and reason about. A
// production setup would typically use an async bus with retries and
// a dead-letter queue.
type Bus struct {
	mu       sync.RWMutex
	handlers map[string][]event.Handler
}

// NewBus returns an empty in-memory bus.
func NewBus() *Bus {
	return &Bus{handlers: make(map[string][]event.Handler)}
}

// Subscribe implements event.Bus.
func (b *Bus) Subscribe(eventType string, h event.Handler) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.handlers[eventType] = append(b.handlers[eventType], h)
}

// Publish implements event.Bus. Handlers are invoked in registration
// order. The bus is single-process: there is no fan-out across nodes.
func (b *Bus) Publish(ctx context.Context, env event.Envelope) error {
	b.mu.RLock()
	handlers := append([]event.Handler(nil), b.handlers[env.Payload.EventType()]...)
	b.mu.RUnlock()

	var firstErr error
	for _, h := range handlers {
		if err := h(ctx, env); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
