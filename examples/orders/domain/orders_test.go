package domain_test

import (
	"errors"
	"testing"

	"github.com/dmitrysharkov/goaxon/aggregate/aggregatetest"
	"github.com/dmitrysharkov/goaxon/examples/orders/domain"
)

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

func TestPlaceWithBadAmountFails(t *testing.T) {
	test := aggregatetest.New[*domain.Order](t, domain.NewOrder)
	test.
		When(func(o *domain.Order) error { return o.Place("Alice", 0) }).
		ThenError(errors.New("amount must be positive"))
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
