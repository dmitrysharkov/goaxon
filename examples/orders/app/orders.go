// Package app exposes the orders bounded context as a typed
// application service. Adapters (HTTP, CLI, message queue, etc.) call
// these methods instead of touching the command/query bus directly —
// the bus stays as the dispatch mechanism beneath, but the service
// layer gives callers a stable typed entry point and centralises
// error mapping.
package app

import (
	"context"
	"errors"

	"github.com/dmitrysharkov/goaxon/command"
	"github.com/dmitrysharkov/goaxon/event"
	"github.com/dmitrysharkov/goaxon/examples/orders/domain"
	"github.com/dmitrysharkov/goaxon/query"
	"github.com/google/uuid"
)

// ErrNotFound collapses the framework's two "not found" cases into
// one app-level sentinel: write-side "no events for that aggregate
// yet" (event.ErrStreamNotFound) and read-side "no projection record
// for that ID" (domain.ErrNotFound). Adapters errors.Is once and map
// to 404 / similar.
var ErrNotFound = errors.New("order not found")

// Orders is the application service for the orders bounded context.
type Orders struct {
	commands *command.Bus
	queries  *query.Bus
}

// New builds the command/query buses, wires the domain handlers and
// the projection against the given event bus and store, and returns a
// typed service ready to use. This is the composition root for the
// bounded context — adapters don't need to touch command, query, or
// domain.Wire directly.
func New(events event.Bus, store event.Store) *Orders {
	commands := command.New()
	queries := query.New()
	domain.Wire(commands, queries, events, store)
	return &Orders{commands: commands, queries: queries}
}

// PlaceOrder generates a UUIDv7 for the new order and dispatches
// the PlaceOrder command. Returns the new order's ID on success.
func (o *Orders) PlaceOrder(ctx context.Context, customer string, amount int) (uuid.UUID, error) {
	id := uuid.Must(uuid.NewV7())
	if _, err := command.Send[domain.PlaceOrder, struct{}](ctx, o.commands,
		domain.PlaceOrder{OrderID: id, Customer: customer, Amount: amount},
	); err != nil {
		return uuid.Nil, err
	}
	return id, nil
}

// ShipOrder dispatches the ShipOrder command. Returns ErrNotFound if
// the order doesn't exist; other errors are domain validation
// failures (already shipped, etc.).
func (o *Orders) ShipOrder(ctx context.Context, id uuid.UUID) error {
	_, err := command.Send[domain.ShipOrder, struct{}](ctx, o.commands,
		domain.ShipOrder{OrderID: id},
	)
	if errors.Is(err, event.ErrStreamNotFound) {
		return ErrNotFound
	}
	return err
}

// GetOrder fetches the read-model summary for an order. Returns
// ErrNotFound if no record exists for the ID.
func (o *Orders) GetOrder(ctx context.Context, id uuid.UUID) (domain.OrderSummary, error) {
	summary, err := query.Ask[domain.GetOrderSummary, domain.OrderSummary](ctx, o.queries,
		domain.GetOrderSummary{OrderID: id},
	)
	if errors.Is(err, domain.ErrNotFound) {
		return domain.OrderSummary{}, ErrNotFound
	}
	return summary, err
}
