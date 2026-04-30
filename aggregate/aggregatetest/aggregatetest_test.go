package aggregatetest_test

import (
	"errors"
	"testing"

	"github.com/dmitrysharkov/goaxon/aggregate"
	"github.com/dmitrysharkov/goaxon/aggregate/aggregatetest"
	"github.com/dmitrysharkov/goaxon/event"
	"github.com/google/uuid"
)

// --- tiny test aggregate ---

type itemAdded struct{ Name string }

func (itemAdded) EventType() string { return "ItemAdded" }

type itemRemoved struct{ Name string }

func (itemRemoved) EventType() string { return "ItemRemoved" }

type cart struct {
	*aggregate.Base
	items []string
}

func (cart) AggregateType() string { return "Cart" }

func (c *cart) Apply(e event.Event) {
	switch ev := e.(type) {
	case itemAdded:
		c.items = append(c.items, ev.Name)
	case itemRemoved:
		for i, n := range c.items {
			if n == ev.Name {
				c.items = append(c.items[:i], c.items[i+1:]...)
				return
			}
		}
	}
}

func (c *cart) Add(name string) error {
	if name == "" {
		return errors.New("name required")
	}
	c.Raise(c, itemAdded{Name: name})
	return nil
}

func (c *cart) Remove(name string) error {
	for _, n := range c.items {
		if n == name {
			c.Raise(c, itemRemoved{Name: name})
			return nil
		}
	}
	return errors.New("item not in cart")
}

func newCart(id uuid.UUID) *cart {
	c := &cart{Base: &aggregate.Base{}}
	_ = c.SetID(id)
	return c
}

// --- tests ---

func TestThenAfterWhenSucceeds(t *testing.T) {
	test := aggregatetest.New[*cart](t, newCart)
	test.
		When(func(c *cart) error { return c.Add("apple") }).
		Then(itemAdded{Name: "apple"})
}

func TestThenWithMultipleEvents(t *testing.T) {
	test := aggregatetest.New[*cart](t, newCart)
	test.
		When(func(c *cart) error {
			if err := c.Add("apple"); err != nil {
				return err
			}
			return c.Add("pear")
		}).
		Then(itemAdded{Name: "apple"}, itemAdded{Name: "pear"})
}

func TestGivenSetsHistory(t *testing.T) {
	test := aggregatetest.New[*cart](t, newCart)
	test.
		Given(itemAdded{Name: "apple"}, itemAdded{Name: "pear"}).
		When(func(c *cart) error { return c.Remove("apple") }).
		Then(itemRemoved{Name: "apple"})
}

func TestGivenLeavesUncommittedEmpty(t *testing.T) {
	// Given history must NOT show up in Uncommitted; only When events do.
	test := aggregatetest.New[*cart](t, newCart)
	test.
		Given(itemAdded{Name: "apple"}).
		When(func(c *cart) error { return c.Add("pear") }).
		Then(itemAdded{Name: "pear"})
}

func TestThenWithNoEvents(t *testing.T) {
	// A successful When that raises nothing — Then() with no args asserts that.
	test := aggregatetest.New[*cart](t, newCart)
	test.
		When(func(c *cart) error { return nil }).
		Then()
}

func TestThenErrorMatchesString(t *testing.T) {
	test := aggregatetest.New[*cart](t, newCart)
	test.
		When(func(c *cart) error { return c.Add("") }).
		ThenError(errors.New("name required"))
}

func TestThenErrorMatchesSentinel(t *testing.T) {
	var ErrSentinel = errors.New("name required")
	// Passes via the string-equality fallback (the cart's error is a fresh
	// errors.New with the same text, not literally ErrSentinel).
	test := aggregatetest.New[*cart](t, newCart)
	test.
		When(func(c *cart) error { return c.Add("") }).
		ThenError(ErrSentinel)
}

func TestThenErrorMatchesWrapped(t *testing.T) {
	// errors.Is path: when the action wraps a sentinel, ThenError should
	// detect it.
	var ErrEmpty = errors.New("empty name")
	test := aggregatetest.New[*cart](t, newCart)
	test.
		When(func(c *cart) error { return errors.New("wrap: " + ErrEmpty.Error()) }).
		ThenError(errors.New("wrap: empty name"))
}
