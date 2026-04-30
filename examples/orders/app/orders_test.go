package app_test

import (
	"context"
	"errors"
	"testing"

	"github.com/dmitrysharkov/goaxon/examples/orders/app"
	"github.com/dmitrysharkov/goaxon/store/memory"
	"github.com/google/uuid"
)

func newApp(t *testing.T) *app.Orders {
	t.Helper()
	return app.New(memory.NewBus(), memory.NewStore())
}

func TestPlaceOrder(t *testing.T) {
	orders := newApp(t)
	id, err := orders.PlaceOrder(context.Background(), "Alice", 4200)
	if err != nil {
		t.Fatal(err)
	}
	if id == uuid.Nil {
		t.Fatal("got nil id")
	}
}

func TestPlaceOrderInvalidAmount(t *testing.T) {
	orders := newApp(t)
	_, err := orders.PlaceOrder(context.Background(), "Alice", 0)
	if err == nil {
		t.Fatal("expected error")
	}
	if err.Error() != "amount must be positive" {
		t.Fatalf("got %v, want 'amount must be positive'", err)
	}
}

func TestShipUnknownOrderReturnsErrNotFound(t *testing.T) {
	orders := newApp(t)
	err := orders.ShipOrder(context.Background(), uuid.Must(uuid.NewV7()))
	if !errors.Is(err, app.ErrNotFound) {
		t.Fatalf("got %v, want app.ErrNotFound", err)
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
	// The order exists — error must NOT be ErrNotFound.
	if errors.Is(err, app.ErrNotFound) {
		t.Fatalf("ErrNotFound wrong here; got %v", err)
	}
}

func TestGetOrderUnknownReturnsErrNotFound(t *testing.T) {
	orders := newApp(t)
	_, err := orders.GetOrder(context.Background(), uuid.Must(uuid.NewV7()))
	if !errors.Is(err, app.ErrNotFound) {
		t.Fatalf("got %v, want app.ErrNotFound", err)
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
	if summary.OrderID != id || summary.Customer != "Alice" || summary.Amount != 4200 || summary.Shipped {
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
