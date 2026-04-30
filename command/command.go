// Package command implements a type-safe command bus.
//
// Commands are imperatives ("PlaceOrder", "ShipParcel"). Unlike events,
// each command type has exactly one handler, and the handler may return
// a result. Use the bus when you want loose coupling between request
// origins (HTTP, CLI, gRPC) and aggregate logic.
package command

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
)

// Command is the marker interface every command type must satisfy.
// Use a CommandType method (rather than reflection alone) so handler
// registration is robust against type renames.
type Command interface {
	CommandType() string
}

// Handler processes a single command of type C and optionally returns
// a result of type R. Use NoResult for R when no return value is needed.
type Handler[C Command, R any] func(ctx context.Context, cmd C) (R, error)

// NoResult is the conventional R for commands that don't produce a
// return value. It's an alias for struct{}, so existing code using
// `struct{}` literally keeps working — the alias is purely a
// readability cue at call sites: `command.Send[PlaceOrder, command.NoResult]`
// reads better than `command.Send[PlaceOrder, struct{}]`.
type NoResult = struct{}

// Bus routes commands to their registered handler. It's safe for
// concurrent use; registration is expected at startup, dispatch at
// request time.
type Bus struct {
	mu       sync.RWMutex
	handlers map[string]any // type name -> Handler[C, R] (boxed)
}

// New returns an empty command bus.
func New() *Bus {
	return &Bus{handlers: make(map[string]any)}
}

// ErrHandlerNotFound is returned by Send when no handler is registered
// for a command type.
var ErrHandlerNotFound = errors.New("command: no handler registered")

// Register binds a handler to command type C. Panics if a handler for
// C is already registered — duplicate handlers almost always indicate
// a bug, and silently overwriting would hide it.
func Register[C Command, R any](bus *Bus, h Handler[C, R]) {
	var zero C
	name := zero.CommandType()

	bus.mu.Lock()
	defer bus.mu.Unlock()
	if _, exists := bus.handlers[name]; exists {
		panic(fmt.Sprintf("command: handler for %q already registered", name))
	}
	bus.handlers[name] = h
}

// Send dispatches cmd to its registered handler and returns the typed
// result. The R type parameter must match the type used at Register
// time; a mismatch is reported as an error rather than a panic so
// callers can recover.
func Send[C Command, R any](ctx context.Context, bus *Bus, cmd C) (R, error) {
	var zero R
	name := cmd.CommandType()

	bus.mu.RLock()
	raw, ok := bus.handlers[name]
	bus.mu.RUnlock()
	if !ok {
		return zero, fmt.Errorf("%w: %s", ErrHandlerNotFound, name)
	}

	h, ok := raw.(Handler[C, R])
	if !ok {
		return zero, fmt.Errorf("command: handler for %s has type %s, not Handler[%T, %T]",
			name, reflect.TypeOf(raw), cmd, zero)
	}

	return h(ctx, cmd)
}
