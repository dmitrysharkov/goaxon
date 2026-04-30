package domain_test

import (
	"errors"
	"testing"

	"github.com/dmitrysharkov/goaxon/aggregate/aggregatetest"
	"github.com/dmitrysharkov/goaxon/examples/orders/domain"
	"github.com/dmitrysharkov/goaxon/types/maybe"
)

// noNotes is a shorthand for the absent-Notes case used in tests
// where the optional field doesn't matter.
func noNotes() maybe.Maybe[domain.Notes] { return maybe.None[domain.Notes]() }

// ---------- VO parser tests ----------

func TestParseCustomerNameRejectsEmpty(t *testing.T) {
	if _, err := domain.ParseCustomerName(""); err == nil {
		t.Fatal("expected error on empty input")
	}
	if _, err := domain.ParseCustomerName("   "); err == nil {
		t.Fatal("expected error on whitespace-only input")
	}
}

func TestParseCustomerNameTrimsWhitespace(t *testing.T) {
	got, err := domain.ParseCustomerName("  Alice  ")
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

func TestParseNotesRejectsEmpty(t *testing.T) {
	if _, err := domain.ParseNotes(""); err == nil {
		t.Fatal("expected error on empty input")
	}
}

func TestParseNotesRejectsTooLong(t *testing.T) {
	long := make([]byte, 501)
	for i := range long {
		long[i] = 'x'
	}
	if _, err := domain.ParseNotes(string(long)); err == nil {
		t.Fatal("expected error on >500 chars")
	}
}

func TestParseNotesAccepts(t *testing.T) {
	got, err := domain.ParseNotes("ring the doorbell")
	if err != nil {
		t.Fatal(err)
	}
	if got != "ring the doorbell" {
		t.Fatalf("got %q", got)
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
		When(func(o *domain.Order) error { return o.Place("Alice", 4200, noNotes()) }).
		Then(domain.OrderPlaced{CustomerName: "Alice", Amount: 4200, Notes: noNotes()})
}

func TestPlaceFreshOrderWithNotes(t *testing.T) {
	test := aggregatetest.New[*domain.Order](t, domain.NewOrder)
	notes := maybe.Some(domain.Notes("ring the doorbell"))
	test.
		When(func(o *domain.Order) error { return o.Place("Alice", 4200, notes) }).
		Then(domain.OrderPlaced{CustomerName: "Alice", Amount: 4200, Notes: notes})
}

func TestPlaceAlreadyPlacedFails(t *testing.T) {
	test := aggregatetest.New[*domain.Order](t, domain.NewOrder)
	test.
		Given(domain.OrderPlaced{CustomerName: "Alice", Amount: 4200, Notes: noNotes()}).
		When(func(o *domain.Order) error { return o.Place("Bob", 100, noNotes()) }).
		ThenError(errors.New("order already placed"))
}

func TestShipPlacedOrder(t *testing.T) {
	test := aggregatetest.New[*domain.Order](t, domain.NewOrder)
	test.
		Given(domain.OrderPlaced{CustomerName: "Alice", Amount: 4200, Notes: noNotes()}).
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
			domain.OrderPlaced{CustomerName: "Alice", Amount: 4200, Notes: noNotes()},
			domain.OrderShipped{},
		).
		When(func(o *domain.Order) error { return o.Ship() }).
		ThenError(errors.New("order already shipped"))
}
