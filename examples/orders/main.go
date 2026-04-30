// Package main shows goaxon driving the orders domain in-process: no
// HTTP, no DB, just direct calls into the application service. The
// aggregate, commands, events, value objects, and projection live in
// ./domain; the typed application surface lives in ./app. See
// ../orders-http for a different driving adapter (chi) against the
// same app + domain.
package main

import (
	"context"
	"fmt"

	"github.com/dmitrysharkov/goaxon/examples/orders/app"
	"github.com/dmitrysharkov/goaxon/store/memory"
	"github.com/google/uuid"
)

func main() {
	ctx := context.Background()

	// Storage backends (in-memory).
	store := memory.NewStore()
	eventBus := memory.NewBus()

	// Composition root for the bounded context.
	orders := app.New(eventBus, store)

	// Drive it through the typed service using only standard Go
	// types — strings/ints. The app layer parses these into VOs
	// (Customer, Amount, UUID) before dispatching commands.
	id, err := orders.PlaceOrder(ctx, "Alice", 4200)
	if err != nil {
		panic(err)
	}
	if err := orders.ShipOrder(ctx, id); err != nil {
		panic(err)
	}

	summary, err := orders.GetOrder(ctx, id)
	if err != nil {
		panic(err)
	}
	fmt.Printf("Order summary: %+v\n", summary)

	// Inspect the event stream (we still have to give Load a uuid.UUID
	// because the framework's API is typed; the conversion is just a
	// parse since we know id is well-formed).
	parsed, _ := uuid.Parse(id)
	envs, _ := store.Load(ctx, parsed)
	fmt.Printf("Stored %d events:\n", len(envs))
	for _, env := range envs {
		fmt.Printf("  seq=%d type=%s\n", env.Sequence, env.Payload.EventType())
	}
}
