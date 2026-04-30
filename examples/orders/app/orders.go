// Package app exposes the orders bounded context as a typed
// application service. Adapters (HTTP, CLI, message queue, etc.) call
// these methods using only standard Go types — strings, ints, etc. —
// and the app layer parses them into domain value objects before
// dispatching commands. Failures from parsing surface as a
// *validation.Error; failures from the bus or domain surface as
// regular errors.
package app

import (
	"context"
	"errors"

	"github.com/dmitrysharkov/goaxon/command"
	"github.com/dmitrysharkov/goaxon/event"
	"github.com/dmitrysharkov/goaxon/examples/orders/domain"
	"github.com/dmitrysharkov/goaxon/maybe"
	"github.com/dmitrysharkov/goaxon/query"
	"github.com/dmitrysharkov/goaxon/validation"
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

// PlaceOrder parses the inputs, generates a fresh OrderID, and
// dispatches the PlaceOrder command. Returns the new order's ID
// (as a string for adapter convenience) on success, or a
// *validation.Error if any input failed to parse.
//
// note is optional: pass nil for "no note." A non-nil pointer is
// parsed as a domain.Notes VO; an empty *note string is rejected
// (use nil instead). The optional field is carried through the rest
// of the stack as maybe.Maybe[domain.Notes].
func (o *Orders) PlaceOrder(ctx context.Context, customerName string, amount int, note *string) (string, error) {
	v := validation.New()
	name := validation.Field(v, "customer_name", domain.ParseCustomerName, customerName)
	amt := validation.Field(v, "amount", domain.MakeAmountFromCents, amount)

	notes := maybe.None[domain.Notes]()
	if note != nil {
		parsed := validation.Field(v, "notes", domain.ParseNotes, *note)
		if v.Err() == nil {
			notes = maybe.Some(parsed)
		}
	}

	if err := v.Err(); err != nil {
		return "", err
	}

	id := domain.NewOrderID()
	if _, err := command.Send[domain.PlaceOrder, struct{}](ctx, o.commands,
		domain.PlaceOrder{OrderID: id, CustomerName: name, Amount: amt, Notes: notes},
	); err != nil {
		return "", err
	}
	return id.String(), nil
}

// ShipOrder parses the order ID and dispatches ShipOrder. Returns
// ErrNotFound if the order doesn't exist, *validation.Error if the
// id isn't a valid UUID, or the underlying domain error otherwise
// (e.g. already shipped).
func (o *Orders) ShipOrder(ctx context.Context, idStr string) error {
	v := validation.New()
	id := validation.Field(v, "id", domain.ParseOrderID, idStr)
	if err := v.Err(); err != nil {
		return err
	}

	_, err := command.Send[domain.ShipOrder, struct{}](ctx, o.commands,
		domain.ShipOrder{OrderID: id},
	)
	if errors.Is(err, event.ErrStreamNotFound) {
		return ErrNotFound
	}
	return err
}

// GetOrder parses the order ID and fetches the read-model summary.
// Returns ErrNotFound if no record exists for the ID, or
// *validation.Error if the id isn't a valid UUID.
func (o *Orders) GetOrder(ctx context.Context, idStr string) (domain.OrderSummary, error) {
	v := validation.New()
	id := validation.Field(v, "id", domain.ParseOrderID, idStr)
	if err := v.Err(); err != nil {
		return domain.OrderSummary{}, err
	}

	summary, err := query.Ask[domain.GetOrderSummary, domain.OrderSummary](ctx, o.queries,
		domain.GetOrderSummary{OrderID: id},
	)
	if errors.Is(err, domain.ErrNotFound) {
		return domain.OrderSummary{}, ErrNotFound
	}
	return summary, err
}
