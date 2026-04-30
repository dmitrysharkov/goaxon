// Package aggregatetest provides a Given/When/Then harness for unit
// testing aggregate command logic without a real store or bus.
//
//	test := aggregatetest.New[*Order](t, NewOrder)
//	test.
//	    Given(OrderPlaced{Customer: "Alice", Amount: 100}).
//	    When(func(o *Order) error { return o.Ship() }).
//	    Then(OrderShipped{})
//
// Given replays events through Apply only — it's a black-box use of
// the aggregate's public surface, no internals required. The aggregate
// you'd test must already follow CLAUDE.md's rule that Apply is
// deterministic and side-effect-free; that's the only assumption.
//
// Versions and uncommitted bookkeeping are not maintained by Given,
// because tests never reach Save. When raises events as normal (via
// aggregate.Base.Raise), so Uncommitted() then contains exactly the
// new events for Then to inspect.
package aggregatetest

import (
	"errors"
	"reflect"
	"testing"

	"github.com/dmitrysharkov/goaxon/aggregate"
	"github.com/dmitrysharkov/goaxon/event"
	"github.com/google/uuid"
)

// AggregateTest drives an aggregate of type A through one
// Given/When/Then cycle. Build with New, chain
// Given → When → (Then | ThenError).
type AggregateTest[A aggregate.Root] struct {
	t       *testing.T
	agg     A
	err     error
	invoked bool
}

// New constructs an AggregateTest by calling factory with a freshly
// generated UUIDv7. The factory is the same one you'd pass to
// aggregate.NewRepository.
func New[A aggregate.Root](t *testing.T, factory func(uuid.UUID) A) *AggregateTest[A] {
	t.Helper()
	return &AggregateTest[A]{
		t:   t,
		agg: factory(uuid.Must(uuid.NewV7())),
	}
}

// Given replays events as committed history by calling Apply on the
// aggregate. State mutates; uncommitted stays empty (the framework's
// Raise path is bypassed). Call before When.
func (a *AggregateTest[A]) Given(events ...event.Event) *AggregateTest[A] {
	a.t.Helper()
	for _, e := range events {
		a.agg.Apply(e)
	}
	return a
}

// When runs the command-equivalent action and captures any error it
// returns. Events the action raises (via aggregate.Base.Raise) end up
// in Uncommitted, ready for Then to inspect.
//
// Use a closure of shape func(A) error. For void aggregate methods,
// wrap and return nil:
//
//	test.When(func(o *Order) error {
//	    o.SomeVoidMethod()
//	    return nil
//	})
func (a *AggregateTest[A]) When(action func(A) error) *AggregateTest[A] {
	a.t.Helper()
	a.invoked = true
	a.err = action(a.agg)
	return a
}

// Then asserts the action raised exactly the listed events (in order)
// and returned a nil error. Pass no arguments to assert "no events
// were raised."
//
// Comparison is reflect.DeepEqual; events must match exactly,
// including unexported fields.
func (a *AggregateTest[A]) Then(expected ...event.Event) {
	a.t.Helper()
	if !a.invoked {
		a.t.Fatal("aggregatetest: Then called without When")
	}
	if a.err != nil {
		a.t.Fatalf("aggregatetest: expected no error, got %v", a.err)
	}
	got := a.uncommitted()
	if len(got) != len(expected) {
		a.t.Fatalf("aggregatetest: expected %d event(s), got %d:\nexpected: %#v\n     got: %#v",
			len(expected), len(got), expected, got)
	}
	for i := range expected {
		if !reflect.DeepEqual(got[i], expected[i]) {
			a.t.Fatalf("aggregatetest: event %d mismatch:\nexpected: %#v\n     got: %#v",
				i, expected[i], got[i])
		}
	}
}

// ThenError asserts the action returned a non-nil error matching want.
// It tries errors.Is(got, want) first (works for sentinel errors), then
// falls back to string equality so ad-hoc errors.New(...) messages from
// the domain match cleanly.
func (a *AggregateTest[A]) ThenError(want error) {
	a.t.Helper()
	if !a.invoked {
		a.t.Fatal("aggregatetest: ThenError called without When")
	}
	if a.err == nil {
		a.t.Fatalf("aggregatetest: expected error %q, got nil", want)
	}
	if errors.Is(a.err, want) {
		return
	}
	if a.err.Error() == want.Error() {
		return
	}
	a.t.Fatalf("aggregatetest: error mismatch:\nexpected: %v\n     got: %v", want, a.err)
}

// uncommitted reads the aggregate's uncommitted events via the public
// Uncommitted() method promoted from *aggregate.Base. Aggregates that
// don't embed *Base can't be tested with this helper — same rule as
// aggregate.Repository.
func (a *AggregateTest[A]) uncommitted() []event.Event {
	a.t.Helper()
	type uncommittedReader interface {
		Uncommitted() []event.Event
	}
	r, ok := any(a.agg).(uncommittedReader)
	if !ok {
		a.t.Fatalf("aggregatetest: %T must embed *aggregate.Base", a.agg)
	}
	return r.Uncommitted()
}
