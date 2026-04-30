package domain_test

import (
	"errors"
	"testing"

	"github.com/dmitrysharkov/goaxon/aggregate/aggregatetest"
	"github.com/dmitrysharkov/goaxon/examples/orders/domain"
)

// ---------- VO parser tests ----------

func TestParseCustomerRejectsEmpty(t *testing.T) {
	if _, err := domain.ParseCustomer(""); err == nil {
		t.Fatal("expected error on empty input")
	}
	if _, err := domain.ParseCustomer("   "); err == nil {
		t.Fatal("expected error on whitespace-only input")
	}
}

func TestParseCustomerTrimsWhitespace(t *testing.T) {
	got, err := domain.ParseCustomer("  Alice  ")
	if err != nil {
		t.Fatal(err)
	}
	if got != "Alice" {
		t.Fatalf("got %q, want %q", got, "Alice")
	}
}

func TestMakeAmountFromCentsRejectsNonPositive(t *testing.T) {
	if _, err := domain.MakeAmountFromCents(0); err == nil {
		t.Fatal("expected error on zero")
	}
	if _, err := domain.MakeAmountFromCents(-1); err == nil {
		t.Fatal("expected error on negative")
	}
}

func TestMakeAmountFromCents(t *testing.T) {
	a, err := domain.MakeAmountFromCents(4200)
	if err != nil {
		t.Fatal(err)
	}
	if a.Cents() != 4200 {
		t.Fatalf("got %d cents, want 4200", a.Cents())
	}
}

// ---------- Aggregate behaviour tests ----------
//
// Note: aggregate-level "amount must be positive" / "customer must not
// be empty" tests are gone — VOs make those states unrepresentable.
// What remains is state-transition logic (already placed, not yet
// placed, etc.).

func TestPlaceFreshOrder(t *testing.T) {
	test := aggregatetest.New[*domain.Order](t, domain.NewOrder)
	test.
		When(func(o *domain.Order) error { return o.Place("Alice", 4200) }).
		Then(domain.OrderPlaced{Customer: "Alice", Amount: 4200})
}

func TestPlaceAlreadyPlacedFails(t *testing.T) {
	test := aggregatetest.New[*domain.Order](t, domain.NewOrder)
	test.
		Given(domain.OrderPlaced{Customer: "Alice", Amount: 4200}).
		When(func(o *domain.Order) error { return o.Place("Bob", 100) }).
		ThenError(errors.New("order already placed"))
}

func TestShipPlacedOrder(t *testing.T) {
	test := aggregatetest.New[*domain.Order](t, domain.NewOrder)
	test.
		Given(domain.OrderPlaced{Customer: "Alice", Amount: 4200}).
		When(func(o *domain.Order) error { return o.Ship() }).
		Then(domain.OrderShipped{})
}

func TestShipUnplacedOrderFails(t *testing.T) {
	test := aggregatetest.New[*domain.Order](t, domain.NewOrder)
	test.
		When(func(o *domain.Order) error { return o.Ship() }).
		ThenError(errors.New("cannot ship an order that hasn't been placed"))
}

func TestShipTwiceFails(t *testing.T) {
	test := aggregatetest.New[*domain.Order](t, domain.NewOrder)
	test.
		Given(
			domain.OrderPlaced{Customer: "Alice", Amount: 4200},
			domain.OrderShipped{},
		).
		When(func(o *domain.Order) error { return o.Ship() }).
		ThenError(errors.New("order already shipped"))
}
