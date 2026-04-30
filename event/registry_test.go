package event_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/dmitrysharkov/goaxon/event"
)

type itemAdded struct {
	Name string
	Qty  int
}

func (itemAdded) EventType() string { return "ItemAdded" }

type itemRemoved struct {
	Name string
}

func (itemRemoved) EventType() string { return "ItemRemoved" }

func TestRegistryRoundTrip(t *testing.T) {
	r := event.NewRegistry()
	event.Register[itemAdded](r)

	got, err := r.Unmarshal("ItemAdded", []byte(`{"Name":"apple","Qty":3}`))
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	v, ok := got.(itemAdded)
	if !ok {
		t.Fatalf("got %T, want itemAdded", got)
	}
	if v.Name != "apple" || v.Qty != 3 {
		t.Fatalf("got %+v", v)
	}
}

func TestRegistryUnknownType(t *testing.T) {
	r := event.NewRegistry()
	_, err := r.Unmarshal("Nope", []byte(`{}`))
	if err == nil || !strings.Contains(err.Error(), "not registered") {
		t.Fatalf("got %v, want unregistered error", err)
	}
}

func TestRegistryDuplicatePanic(t *testing.T) {
	r := event.NewRegistry()
	event.Register[itemAdded](r)

	defer func() {
		got := recover()
		if got == nil {
			t.Fatal("expected panic on duplicate registration")
		}
	}()
	event.Register[itemAdded](r)
}

func TestRegistryMultipleTypes(t *testing.T) {
	r := event.NewRegistry()
	event.Register[itemAdded](r)
	event.Register[itemRemoved](r)

	if _, err := r.Unmarshal("ItemAdded", []byte(`{"Name":"a","Qty":1}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Unmarshal("ItemRemoved", []byte(`{"Name":"a"}`)); err != nil {
		t.Fatal(err)
	}
}

func TestRegistryBadJSON(t *testing.T) {
	r := event.NewRegistry()
	event.Register[itemAdded](r)

	_, err := r.Unmarshal("ItemAdded", []byte(`not json`))
	if err == nil {
		t.Fatal("expected error on bad JSON")
	}
	if errors.Is(err, errors.New("nope")) {
		// just exercising that errors.Is doesn't accidentally match
	}
}
