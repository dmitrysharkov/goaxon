// Package main shows goaxon driving the orders domain in-process: no
// HTTP, no DB, just direct calls into the application service. The
// aggregate, commands, events, and projection live in ./domain; the
// typed application surface lives in ./app. See ../orders-http for a
// different driving adapter (chi) against the same app + domain.
package main

import (
	"context"
	"fmt"

	"github.com/dmitrysharkov/goaxon/examples/orders/app"
	"github.com/dmitrysharkov/goaxon/store/memory"
)

func main() {
	ctx := context.Background()

	// Storage backends (in-memory).
	store := memory.NewStore()
	eventBus := memory.NewBus()

	// Composition root for the bounded context.
	orders := app.New(eventBus, store)

	// Drive it through the typed service.
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

	envs, _ := store.Load(ctx, id)
	fmt.Printf("Stored %d events:\n", len(envs))
	for _, env := range envs {
		fmt.Printf("  seq=%d type=%s\n", env.Sequence, env.Payload.EventType())
	}
}
