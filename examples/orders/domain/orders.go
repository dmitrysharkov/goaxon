// Package domain holds the orders aggregate, its commands, events,
// read model, and the value objects (VOs) the aggregate operates on.
//
// VOs (CustomerName, Amount, OrderID) carry their own validity
// invariants — they can only be constructed via Parse* / Make*From*
// functions. Once you have one, you know it's valid. Aggregate command
// methods take VOs, so they no longer need to re-check field-level
// validity; they only check state-transition rules (e.g. "can't ship
// before placing").
//
// Naming: Parse* takes a string input; MakeXFromY takes a typed
// non-string input. Both return (T, error) and validate.
//
// Note on naming: "CustomerName" not "Customer". The customer is an
// entity (their own aggregate, billing address, etc.); what we capture
// on the order is the customer's name at order time. Same reasoning
// would apply if we ever modelled Email vs EmailAddress, etc.
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
	"github.com/dmitrysharkov/goaxon/typ"
	"github.com/dmitrysharkov/goaxon/query"
	"github.com/google/uuid"
)

// ---------- Value Objects ----------

// OrderID is a typed wrapper around uuid.UUID. It exists so the type
// system catches accidents like "CustomerID passed where OrderID was
// expected" — uuid.UUID alone can't do that. The trade-off is the
// MarshalJSON / UnmarshalJSON / String boilerplate per ID type; in a
// real domain with many aggregates you'd codegen these.
type OrderID uuid.UUID

// ParseOrderID parses the dashed-string UUID form ("01900000-...").
func ParseOrderID(s string) (OrderID, error) {
	u, err := uuid.Parse(s)
	if err != nil {
		return OrderID{}, err
	}
	return OrderID(u), nil
}

// NewOrderID generates a fresh UUIDv7 (time-ordered prefix gives the
// events table good B-tree locality).
func NewOrderID() OrderID { return OrderID(uuid.Must(uuid.NewV7())) }

// String returns the dashed-UUID form.
func (id OrderID) String() string { return uuid.UUID(id).String() }

// MarshalText keeps the wire format as the dashed-UUID string. Without
// this, encoding/json would default-marshal the underlying [16]byte as
// a JSON array of integers. encoding/json picks up TextMarshaler
// automatically, so JSON serialization works through this without
// needing a separate MarshalJSON.
func (id OrderID) MarshalText() ([]byte, error) { return uuid.UUID(id).MarshalText() }

// UnmarshalText parses the dashed-UUID string back to an OrderID.
// This is FORMAT conversion, not domain validation — it's fine for
// replay (see CLAUDE.md "Events are immutable facts; replay trusts
// them").
func (id *OrderID) UnmarshalText(data []byte) error {
	var u uuid.UUID
	if err := u.UnmarshalText(data); err != nil {
		return err
	}
	*id = OrderID(u)
	return nil
}

// CustomerName is the order's customer-name snapshot. Trimmed,
// non-empty.
type CustomerName string

func ParseCustomerName(s string) (CustomerName, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", errors.New("must not be empty")
	}
	return CustomerName(s), nil
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

// Notes is an optional free-text note on an order. When present it
// must be non-empty and at most 500 characters. "Optional" means the
// note can be absent, not that an empty value is valid — absence is
// represented by typ.None[Notes]() in the surrounding fields.
type Notes string

func ParseNotes(s string) (Notes, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", errors.New("must not be empty")
	}
	if len(s) > 500 {
		return "", errors.New("must be at most 500 characters")
	}
	return Notes(s), nil
}

// ---------- Commands ----------

type PlaceOrder struct {
	OrderID      OrderID
	CustomerName CustomerName
	Amount       Amount
	Notes        typ.Maybe[Notes]
}

func (PlaceOrder) CommandType() string { return "PlaceOrder" }

type ShipOrder struct {
	OrderID OrderID
}

func (ShipOrder) CommandType() string { return "ShipOrder" }

// ---------- Events ----------
//
// JSON tags freeze the wire format independently of Go field names —
// renaming `CustomerName` → `Customer` in Go later wouldn't break
// replay of old events. CLAUDE.md's "events are facts" rule applies
// to event SHAPE, not just field values.

type OrderPlaced struct {
	CustomerName CustomerName       `json:"customer_name"`
	Amount       Amount             `json:"amount"`
	Notes        typ.Maybe[Notes] `json:"notes"`
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
	customerName CustomerName
	amount       Amount
	status       orderStatus
}

func (Order) AggregateType() string { return "Order" }

// NewOrder is the factory the repository uses when loading or creating.
// The framework's contract is uuid.UUID; the conversion to OrderID
// happens at the domain boundary.
func NewOrder(id uuid.UUID) *Order {
	o := &Order{Base: &aggregate.Base{}}
	_ = o.SetID(id)
	return o
}

// Apply mutates state in response to events. Pure and deterministic.
func (o *Order) Apply(e event.Event) {
	switch ev := e.(type) {
	case OrderPlaced:
		o.customerName = ev.CustomerName
		o.amount = ev.Amount
		o.status = statusPlaced
	case OrderShipped:
		o.status = statusShipped
	}
}

// Place raises OrderPlaced if the order is new. The VOs are already
// valid by construction, so the only check left here is the state
// transition. Notes is optional — pass typ.None[Notes]() if absent.
func (o *Order) Place(name CustomerName, amount Amount, notes typ.Maybe[Notes]) error {
	if o.status != statusNew {
		return errors.New("order already placed")
	}
	o.Raise(o, OrderPlaced{CustomerName: name, Amount: amount, Notes: notes})
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
	OrderID      OrderID            `json:"order_id"`
	CustomerName CustomerName       `json:"customer_name"`
	Amount       Amount             `json:"amount"`
	Notes        typ.Maybe[Notes] `json:"notes"`
	Shipped      bool               `json:"shipped"`
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
		OrderID:      OrderID(env.AggregateID),
		CustomerName: ev.CustomerName,
		Amount:       ev.Amount,
		Notes:        ev.Notes,
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
	OrderID OrderID
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

	command.Register(commandBus, func(ctx context.Context, cmd PlaceOrder) (typ.Unit, error) {
		rawID := uuid.UUID(cmd.OrderID)
		o, err := repo.Load(ctx, rawID)
		switch {
		case errors.Is(err, event.ErrStreamNotFound):
			o = NewOrder(rawID)
		case err != nil:
			return typ.Unit{}, err
		}
		if err := o.Place(cmd.CustomerName, cmd.Amount, cmd.Notes); err != nil {
			return typ.Unit{}, err
		}
		return typ.Unit{}, repo.Save(ctx, o)
	})

	command.Register(commandBus, func(ctx context.Context, cmd ShipOrder) (typ.Unit, error) {
		o, err := repo.Load(ctx, uuid.UUID(cmd.OrderID))
		if err != nil {
			return typ.Unit{}, err
		}
		if err := o.Ship(); err != nil {
			return typ.Unit{}, err
		}
		return typ.Unit{}, repo.Save(ctx, o)
	})

	query.Register(queryBus, func(_ context.Context, q GetOrderSummary) (OrderSummary, error) {
		rec, ok := summary.Get(uuid.UUID(q.OrderID))
		if !ok {
			return OrderSummary{}, fmt.Errorf("%w: %s", ErrNotFound, q.OrderID)
		}
		return rec, nil
	})

	return summary
}
