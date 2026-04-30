// Package domain holds the orders aggregate, its commands, events,
// read model, and the value objects (VOs) the aggregate operates on.
//
// VOs (Customer, Amount) carry their own validity invariants — they
// can only be constructed via Parse* / Make*From* functions. Once you
// have one, you know it's valid. Aggregate command methods take VOs,
// so they no longer need to re-check field-level validity; they only
// check state-transition rules (e.g. "can't ship before placing").
//
// Naming: Parse* takes a string input; MakeXFromY takes a typed
// non-string input. Both return (T, error) and validate.
package domain

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/dmitrysharkov/goaxon/aggregate"
	"github.com/dmitrysharkov/goaxon/command"
	"github.com/dmitrysharkov/goaxon/event"
	"github.com/dmitrysharkov/goaxon/query"
	"github.com/google/uuid"
)

// ---------- Value Objects ----------

// Customer is the order's customer name. Trimmed, non-empty.
type Customer string

func ParseCustomer(s string) (Customer, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", errors.New("must not be empty")
	}
	return Customer(s), nil
}

// Amount is an order amount in cents. Strictly positive.
type Amount int

func MakeAmountFromCents(cents int) (Amount, error) {
	if cents <= 0 {
		return 0, errors.New("must be positive")
	}
	return Amount(cents), nil
}

// Cents returns the amount as an integer cent value.
func (a Amount) Cents() int { return int(a) }

// ---------- Commands ----------

type PlaceOrder struct {
	OrderID  uuid.UUID
	Customer Customer
	Amount   Amount
}

func (PlaceOrder) CommandType() string { return "PlaceOrder" }

type ShipOrder struct {
	OrderID uuid.UUID
}

func (ShipOrder) CommandType() string { return "ShipOrder" }

// ---------- Events ----------

type OrderPlaced struct {
	Customer Customer
	Amount   Amount
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
	customer Customer
	amount   Amount
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

// Place raises OrderPlaced if the order is new. The customer/amount
// VOs are already valid by construction, so the only check left here
// is the state transition.
func (o *Order) Place(customer Customer, amount Amount) error {
	if o.status != statusNew {
		return errors.New("order already placed")
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
	Customer Customer  `json:"customer"`
	Amount   Amount    `json:"amount"`
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
		switch {
		case errors.Is(err, event.ErrStreamNotFound):
			o = NewOrder(cmd.OrderID)
		case err != nil:
			return struct{}{}, err
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
