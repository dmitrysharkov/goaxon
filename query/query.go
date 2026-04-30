// Package query implements a type-safe query bus for the read side
// of a CQRS application.
//
// Queries are read-only requests for projection data. Keeping them on
// a separate bus from commands makes the intent of each call site
// obvious and lets the two sides scale independently.
package query

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
)

// Query is the marker interface for a read-side request.
type Query interface {
	QueryType() string
}

// Handler answers a single query of type Q with a result of type R.
type Handler[Q Query, R any] func(ctx context.Context, q Q) (R, error)

// Bus routes queries to their registered handler.
type Bus struct {
	mu       sync.RWMutex
	handlers map[string]any
}

// New returns an empty query bus.
func New() *Bus {
	return &Bus{handlers: make(map[string]any)}
}

// ErrHandlerNotFound is returned by Ask when no handler is registered
// for a query type.
var ErrHandlerNotFound = errors.New("query: no handler registered")

// Register binds a handler to query type Q.
func Register[Q Query, R any](bus *Bus, h Handler[Q, R]) {
	var zero Q
	name := zero.QueryType()

	bus.mu.Lock()
	defer bus.mu.Unlock()
	if _, exists := bus.handlers[name]; exists {
		panic(fmt.Sprintf("query: handler for %q already registered", name))
	}
	bus.handlers[name] = h
}

// Ask dispatches q to its registered handler.
func Ask[Q Query, R any](ctx context.Context, bus *Bus, q Q) (R, error) {
	var zero R
	name := q.QueryType()

	bus.mu.RLock()
	raw, ok := bus.handlers[name]
	bus.mu.RUnlock()
	if !ok {
		return zero, fmt.Errorf("%w: %s", ErrHandlerNotFound, name)
	}

	h, ok := raw.(Handler[Q, R])
	if !ok {
		return zero, fmt.Errorf("query: handler for %s has type %s, not Handler[%T, %T]",
			name, reflect.TypeOf(raw), q, zero)
	}

	return h(ctx, q)
}
