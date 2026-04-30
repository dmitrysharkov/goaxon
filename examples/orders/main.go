// Package main shows goaxon in action with a small order-management
// domain. We define commands, events, an aggregate, and a read-model
// projection — then exercise them end-to-end.
package main

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/example/goaxon/aggregate"
	"github.com/example/goaxon/command"
	"github.com/example/goaxon/event"
	"github.com/example/goaxon/query"
	"github.com/example/goaxon/store/memory"
)

// ---------- Commands ----------

type PlaceOrder struct {
	OrderID  string
	Customer string
	Amount   int // cents
}

func (PlaceOrder) CommandType() string { return "PlaceOrder" }

type ShipOrder struct {
	OrderID string
}

func (ShipOrder) CommandType() string { return "ShipOrder" }

// ---------- Events ----------

type OrderPlaced struct {
	Customer string
	Amount   int
}

func (OrderPlaced) EventType() string { return "OrderPlaced" }

type OrderShipped struct{}

func (OrderShipped) EventType() string { return "OrderShipped" }

// ---------- Aggregate ----------

type orderStatus int

const (
	statusNew orderStatus = iota
	statusPlaced
	statusShipped
)

type Order struct {
	*aggregate.Base
	customer string
	amount   int
	status   orderStatus
}

func (Order) AggregateType() string { return "Order" }

// newOrder is the factory the repository uses when loading or creating.
func newOrder(id string) *Order {
	o := &Order{Base: &aggregate.Base{}}
	_ = o.SetID(id)
	return o
}

// Apply mutates state in response to events. Pure and deterministic.
func (o *Order) Apply(e event.Event) {
	switch ev := e.(type) {
	case OrderPlaced:
		o.customer = ev.Customer
		o.amount = ev.Amount
		o.status = statusPlaced
	case OrderShipped:
		o.status = statusShipped
	}
}

// Place is invoked by the PlaceOrder command handler. It validates,
// then raises the event — which both updates state and queues it for
// persistence.
func (o *Order) Place(customer string, amount int) error {
	if o.status != statusNew {
		return errors.New("order already placed")
	}
	if amount <= 0 {
		return errors.New("amount must be positive")
	}
	o.Raise(o, OrderPlaced{Customer: customer, Amount: amount})
	return nil
}

func (o *Order) Ship() error {
	switch o.status {
	case statusNew:
		return errors.New("cannot ship an order that hasn't been placed")
	case statusShipped:
		return errors.New("order already shipped")
	}
	o.Raise(o, OrderShipped{})
	return nil
}

// ---------- Read model (projection) ----------

// OrderSummary is the read-model row built from events.
type OrderSummary struct {
	OrderID  string
	Customer string
	Amount   int
	Shipped  bool
}

// summaryProjection is a thread-safe map keyed by order ID. In a real
// system this would back onto a SQL table or a key-value store.
type summaryProjection struct {
	mu      sync.RWMutex
	records map[string]OrderSummary
}

func newSummaryProjection() *summaryProjection {
	return &summaryProjection{records: make(map[string]OrderSummary)}
}

func (p *summaryProjection) onOrderPlaced(_ context.Context, env event.Envelope) error {
	ev := env.Payload.(OrderPlaced)
	p.mu.Lock()
	defer p.mu.Unlock()
	p.records[env.AggregateID] = OrderSummary{
		OrderID:  env.AggregateID,
		Customer: ev.Customer,
		Amount:   ev.Amount,
	}
	return nil
}

func (p *summaryProjection) onOrderShipped(_ context.Context, env event.Envelope) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	rec := p.records[env.AggregateID]
	rec.Shipped = true
	p.records[env.AggregateID] = rec
	return nil
}

// ---------- Queries ----------

type GetOrderSummary struct {
	OrderID string
}

func (GetOrderSummary) QueryType() string { return "GetOrderSummary" }

// ---------- Wiring & demo ----------

func main() {
	ctx := context.Background()

	// Infrastructure
	store := memory.NewStore()
	eventBus := memory.NewBus()
	commandBus := command.New()
	queryBus := query.New()

	// Aggregate repository
	orderRepo := aggregate.NewRepository[*Order](store, eventBus, newOrder)

	// Read-side projection
	summary := newSummaryProjection()
	eventBus.Subscribe("OrderPlaced", summary.onOrderPlaced)
	eventBus.Subscribe("OrderShipped", summary.onOrderShipped)

	// Command handlers
	command.Register(commandBus, func(ctx context.Context, cmd PlaceOrder) (struct{}, error) {
		o, err := orderRepo.Load(ctx, cmd.OrderID)
		if err != nil {
			return struct{}{}, err
		}
		if o.AggregateID() == "" {
			o = newOrder(cmd.OrderID)
		}
		if err := o.Place(cmd.Customer, cmd.Amount); err != nil {
			return struct{}{}, err
		}
		return struct{}{}, orderRepo.Save(ctx, o)
	})

	command.Register(commandBus, func(ctx context.Context, cmd ShipOrder) (struct{}, error) {
		o, err := orderRepo.Load(ctx, cmd.OrderID)
		if err != nil {
			return struct{}{}, err
		}
		if err := o.Ship(); err != nil {
			return struct{}{}, err
		}
		return struct{}{}, orderRepo.Save(ctx, o)
	})

	// Query handler
	query.Register(queryBus, func(_ context.Context, q GetOrderSummary) (OrderSummary, error) {
		summary.mu.RLock()
		defer summary.mu.RUnlock()
		rec, ok := summary.records[q.OrderID]
		if !ok {
			return OrderSummary{}, fmt.Errorf("order %s not found", q.OrderID)
		}
		return rec, nil
	})

	// Drive it
	if _, err := command.Send[PlaceOrder, struct{}](ctx, commandBus, PlaceOrder{
		OrderID: "ord-1", Customer: "Alice", Amount: 4200,
	}); err != nil {
		panic(err)
	}
	if _, err := command.Send[ShipOrder, struct{}](ctx, commandBus, ShipOrder{OrderID: "ord-1"}); err != nil {
		panic(err)
	}

	got, err := query.Ask[GetOrderSummary, OrderSummary](ctx, queryBus, GetOrderSummary{OrderID: "ord-1"})
	if err != nil {
		panic(err)
	}
	fmt.Printf("Order summary: %+v\n", got)

	// And verify the underlying event stream
	envs, _ := store.Load(ctx, "ord-1")
	fmt.Printf("Stored %d events:\n", len(envs))
	for _, env := range envs {
		fmt.Printf("  seq=%d type=%s\n", env.Sequence, env.Payload.EventType())
	}
}
