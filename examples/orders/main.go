// Package main shows goaxon driving the orders domain in-process: no
// HTTP, no DB, just direct command/query dispatch. The aggregate,
// commands, events, and projection live in ./domain — see also the
// HTTP demo at ../orders-http for a different driving adapter against
// the same core.
package main

import (
	"context"
	"fmt"

	"github.com/dmitrysharkov/goaxon/command"
	"github.com/dmitrysharkov/goaxon/examples/orders/domain"
	"github.com/dmitrysharkov/goaxon/query"
	"github.com/dmitrysharkov/goaxon/store/memory"
	"github.com/google/uuid"
)

func main() {
	ctx := context.Background()

	// Infrastructure (in-memory)
	store := memory.NewStore()
	eventBus := memory.NewBus()
	commandBus := command.New()
	queryBus := query.New()

	// Application: register handlers and projection against the buses.
	domain.Wire(commandBus, queryBus, eventBus, store)

	// Drive it. UUIDv7 gets time-ordered prefixes — better B-tree
	// locality if you swap the in-memory store for Postgres.
	orderID := uuid.Must(uuid.NewV7())
	if _, err := command.Send[domain.PlaceOrder, struct{}](ctx, commandBus, domain.PlaceOrder{
		OrderID: orderID, Customer: "Alice", Amount: 4200,
	}); err != nil {
		panic(err)
	}
	if _, err := command.Send[domain.ShipOrder, struct{}](ctx, commandBus, domain.ShipOrder{OrderID: orderID}); err != nil {
		panic(err)
	}

	got, err := query.Ask[domain.GetOrderSummary, domain.OrderSummary](ctx, queryBus, domain.GetOrderSummary{OrderID: orderID})
	if err != nil {
		panic(err)
	}
	fmt.Printf("Order summary: %+v\n", got)

	envs, _ := store.Load(ctx, orderID)
	fmt.Printf("Stored %d events:\n", len(envs))
	for _, env := range envs {
		fmt.Printf("  seq=%d type=%s\n", env.Sequence, env.Payload.EventType())
	}
}
