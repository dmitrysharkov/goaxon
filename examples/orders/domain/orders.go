// Package domain holds the orders aggregate, its commands, events, and
// read model. It's the hexagonal "core" — both the in-process demo
// (../main.go) and the HTTP demo (../../orders-http/main.go) import
// from here. Adapters (HTTP, CLI, message bus) talk to the application
// only through the command/query buses; nothing in this package knows
// or cares which adapter dispatched the command.
package domain

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/dmitrysharkov/goaxon/aggregate"
	"github.com/dmitrysharkov/goaxon/command"
	"github.com/dmitrysharkov/goaxon/event"
	"github.com/dmitrysharkov/goaxon/query"
	"github.com/google/uuid"
)

// ---------- Commands ----------

type PlaceOrder struct {
	OrderID  uuid.UUID
	Customer string
	Amount   int // cents
}

func (PlaceOrder) CommandType() string { return "PlaceOrder" }

type ShipOrder struct {
	OrderID uuid.UUID
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

// NewOrder is the factory the repository uses when loading or creating.
func NewOrder(id uuid.UUID) *Order {
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

// Place validates the request and raises OrderPlaced.
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

// Ship validates state and raises OrderShipped.
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
	OrderID  uuid.UUID `json:"order_id"`
	Customer string    `json:"customer"`
	Amount   int       `json:"amount"`
	Shipped  bool      `json:"shipped"`
}

// SummaryProjection is a thread-safe map keyed by order ID, populated
// from OrderPlaced/OrderShipped events. In a real system this would
// back onto a SQL table or KV store.
type SummaryProjection struct {
	mu      sync.RWMutex
	records map[uuid.UUID]OrderSummary
}

func NewSummaryProjection() *SummaryProjection {
	return &SummaryProjection{records: make(map[uuid.UUID]OrderSummary)}
}

func (p *SummaryProjection) OnOrderPlaced(_ context.Context, env event.Envelope) error {
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

func (p *SummaryProjection) OnOrderShipped(_ context.Context, env event.Envelope) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	rec := p.records[env.AggregateID]
	rec.Shipped = true
	p.records[env.AggregateID] = rec
	return nil
}

// Get returns the summary for the given order ID, if any.
func (p *SummaryProjection) Get(id uuid.UUID) (OrderSummary, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	rec, ok := p.records[id]
	return rec, ok
}

// ---------- Queries ----------

type GetOrderSummary struct {
	OrderID uuid.UUID
}

func (GetOrderSummary) QueryType() string { return "GetOrderSummary" }

// ---------- Errors ----------

// ErrNotFound is returned by GetOrderSummary when the order doesn't
// exist. Adapters can errors.Is against this to decide on 404 vs 500.
var ErrNotFound = errors.New("order not found")

// ---------- Wiring ----------

// Wire registers command handlers, the projection, and the query handler
// against the given infrastructure. Returns the projection for callers
// that want direct read access (the HTTP demo doesn't; it goes through
// the query bus).
func Wire(
	commandBus *command.Bus,
	queryBus *query.Bus,
	eventBus event.Bus,
	store event.Store,
) *SummaryProjection {
	repo := aggregate.NewRepository(store, eventBus, NewOrder)

	summary := NewSummaryProjection()
	eventBus.Subscribe("OrderPlaced", summary.OnOrderPlaced)
	eventBus.Subscribe("OrderShipped", summary.OnOrderShipped)

	command.Register(commandBus, func(ctx context.Context, cmd PlaceOrder) (struct{}, error) {
		o, err := repo.Load(ctx, cmd.OrderID)
		if err != nil {
			return struct{}{}, err
		}
		if o.AggregateID() == uuid.Nil {
			o = NewOrder(cmd.OrderID)
		}
		if err := o.Place(cmd.Customer, cmd.Amount); err != nil {
			return struct{}{}, err
		}
		return struct{}{}, repo.Save(ctx, o)
	})

	command.Register(commandBus, func(ctx context.Context, cmd ShipOrder) (struct{}, error) {
		o, err := repo.Load(ctx, cmd.OrderID)
		if err != nil {
			return struct{}{}, err
		}
		if err := o.Ship(); err != nil {
			return struct{}{}, err
		}
		return struct{}{}, repo.Save(ctx, o)
	})

	query.Register(queryBus, func(_ context.Context, q GetOrderSummary) (OrderSummary, error) {
		rec, ok := summary.Get(q.OrderID)
		if !ok {
			return OrderSummary{}, fmt.Errorf("%w: %s", ErrNotFound, q.OrderID)
		}
		return rec, nil
	})

	return summary
}
