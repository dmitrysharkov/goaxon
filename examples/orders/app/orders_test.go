package app_test

import (
	"context"
	"errors"
	"testing"

	"github.com/dmitrysharkov/goaxon/examples/orders/app"
	"github.com/dmitrysharkov/goaxon/store/memory"
	"github.com/dmitrysharkov/goaxon/validation"
	"github.com/google/uuid"
)

func newApp(t *testing.T) *app.Orders {
	t.Helper()
	return app.New(memory.NewBus(), memory.NewStore())
}

// freshID returns a string-form UUIDv7 — what adapters would pass in.
func freshID() string { return uuid.Must(uuid.NewV7()).String() }

func TestPlaceOrder(t *testing.T) {
	orders := newApp(t)
	id, err := orders.PlaceOrder(context.Background(), "Alice", 4200)
	if err != nil {
		t.Fatal(err)
	}
	if id == "" {
		t.Fatal("got empty id")
	}
	if _, parseErr := uuid.Parse(id); parseErr != nil {
		t.Fatalf("returned id is not a valid UUID: %v", parseErr)
	}
}

func TestPlaceOrderValidationErrors(t *testing.T) {
	orders := newApp(t)
	_, err := orders.PlaceOrder(context.Background(), "", 0)
	if err == nil {
		t.Fatal("expected error")
	}
	var verr *validation.Error
	if !errors.As(err, &verr) {
		t.Fatalf("got %T, want *ValidationError", err)
	}
	if _, ok := verr.Fields["customer_name"]; !ok {
		t.Fatalf("missing customer field error: %+v", verr.Fields)
	}
	if _, ok := verr.Fields["amount"]; !ok {
		t.Fatalf("missing amount field error: %+v", verr.Fields)
	}
}

func TestPlaceOrderInvalidAmountOnly(t *testing.T) {
	orders := newApp(t)
	_, err := orders.PlaceOrder(context.Background(), "Alice", 0)
	var verr *validation.Error
	if !errors.As(err, &verr) {
		t.Fatalf("got %T, want *ValidationError", err)
	}
	if len(verr.Fields) != 1 {
		t.Fatalf("got %d field errors, want 1: %+v", len(verr.Fields), verr.Fields)
	}
}

func TestShipUnknownOrderReturnsErrNotFound(t *testing.T) {
	orders := newApp(t)
	err := orders.ShipOrder(context.Background(), freshID())
	if !errors.Is(err, app.ErrNotFound) {
		t.Fatalf("got %v, want app.ErrNotFound", err)
	}
}

func TestShipBadIDReturnsValidationError(t *testing.T) {
	orders := newApp(t)
	err := orders.ShipOrder(context.Background(), "not-a-uuid")
	var verr *validation.Error
	if !errors.As(err, &verr) {
		t.Fatalf("got %T, want *ValidationError", err)
	}
	if _, ok := verr.Fields["id"]; !ok {
		t.Fatalf("missing id field error: %+v", verr.Fields)
	}
}

func TestShipPlacedOrder(t *testing.T) {
	orders := newApp(t)
	ctx := context.Background()
	id, err := orders.PlaceOrder(ctx, "Alice", 4200)
	if err != nil {
		t.Fatal(err)
	}
	if err := orders.ShipOrder(ctx, id); err != nil {
		t.Fatal(err)
	}
}

func TestShipTwiceFailsButNotAsNotFound(t *testing.T) {
	orders := newApp(t)
	ctx := context.Background()
	id, _ := orders.PlaceOrder(ctx, "Alice", 4200)
	if err := orders.ShipOrder(ctx, id); err != nil {
		t.Fatal(err)
	}
	err := orders.ShipOrder(ctx, id)
	if err == nil {
		t.Fatal("expected error on second ship")
	}
	if errors.Is(err, app.ErrNotFound) {
		t.Fatalf("ErrNotFound wrong here; got %v", err)
	}
	var verr *validation.Error
	if errors.As(err, &verr) {
		t.Fatalf("got ValidationError, want plain domain error: %v", err)
	}
}

func TestGetOrderUnknownReturnsErrNotFound(t *testing.T) {
	orders := newApp(t)
	_, err := orders.GetOrder(context.Background(), freshID())
	if !errors.Is(err, app.ErrNotFound) {
		t.Fatalf("got %v, want app.ErrNotFound", err)
	}
}

func TestGetOrderBadIDReturnsValidationError(t *testing.T) {
	orders := newApp(t)
	_, err := orders.GetOrder(context.Background(), "nope")
	var verr *validation.Error
	if !errors.As(err, &verr) {
		t.Fatalf("got %T, want *ValidationError", err)
	}
}

func TestGetOrderAfterPlace(t *testing.T) {
	orders := newApp(t)
	ctx := context.Background()
	id, err := orders.PlaceOrder(ctx, "Alice", 4200)
	if err != nil {
		t.Fatal(err)
	}
	summary, err := orders.GetOrder(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if summary.OrderID.String() != id ||
		summary.CustomerName != "Alice" ||
		summary.Amount.Cents() != 4200 ||
		summary.Shipped {
		t.Fatalf("got %+v", summary)
	}
}

func TestGetOrderAfterShip(t *testing.T) {
	orders := newApp(t)
	ctx := context.Background()
	id, err := orders.PlaceOrder(ctx, "Alice", 100)
	if err != nil {
		t.Fatal(err)
	}
	if err := orders.ShipOrder(ctx, id); err != nil {
		t.Fatal(err)
	}
	summary, err := orders.GetOrder(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if !summary.Shipped {
		t.Fatalf("expected shipped=true, got %+v", summary)
	}
}
