package maybe_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/dmitrysharkov/goaxon/maybe"
)

func TestSomeIsPresent(t *testing.T) {
	m := maybe.Some(42)
	if !m.IsSome() {
		t.Fatal("expected IsSome true")
	}
	if m.IsNone() {
		t.Fatal("expected IsNone false")
	}
	v, ok := m.Get()
	if !ok || v != 42 {
		t.Fatalf("Get: got (%d, %v), want (42, true)", v, ok)
	}
}

func TestNoneIsAbsent(t *testing.T) {
	m := maybe.None[int]()
	if m.IsSome() {
		t.Fatal("expected IsSome false")
	}
	if !m.IsNone() {
		t.Fatal("expected IsNone true")
	}
	v, ok := m.Get()
	if ok || v != 0 {
		t.Fatalf("Get: got (%d, %v), want (0, false)", v, ok)
	}
}

func TestZeroValueIsNone(t *testing.T) {
	var m maybe.Maybe[string]
	if !m.IsNone() {
		t.Fatal("zero value should be None")
	}
}

func TestOrElse(t *testing.T) {
	if v := maybe.Some("a").OrElse("fallback"); v != "a" {
		t.Fatalf("Some.OrElse: got %q, want %q", v, "a")
	}
	if v := maybe.None[string]().OrElse("fallback"); v != "fallback" {
		t.Fatalf("None.OrElse: got %q, want %q", v, "fallback")
	}
}

func TestMustGet(t *testing.T) {
	if v := maybe.Some(7).MustGet(); v != 7 {
		t.Fatalf("got %d, want 7", v)
	}

	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on MustGet on None")
		}
	}()
	maybe.None[int]().MustGet()
}

func TestMapTransformsSome(t *testing.T) {
	m := maybe.Some(3)
	doubled := maybe.Map(m, func(n int) int { return n * 2 })
	if v, _ := doubled.Get(); v != 6 {
		t.Fatalf("got %d, want 6", v)
	}
}

func TestMapPropagatesNone(t *testing.T) {
	m := maybe.None[int]()
	doubled := maybe.Map(m, func(n int) int { return n * 2 })
	if !doubled.IsNone() {
		t.Fatal("Map(None, f) should be None")
	}
}

func TestMapChangesType(t *testing.T) {
	m := maybe.Some(3)
	asString := maybe.Map(m, func(n int) string { return strings.Repeat("x", n) })
	if v, _ := asString.Get(); v != "xxx" {
		t.Fatalf("got %q, want xxx", v)
	}
}

func TestFlatMapChainsMaybes(t *testing.T) {
	m := maybe.Some(4)
	even := func(n int) maybe.Maybe[int] {
		if n%2 == 0 {
			return maybe.Some(n)
		}
		return maybe.None[int]()
	}
	if v, ok := maybe.FlatMap(m, even).Get(); !ok || v != 4 {
		t.Fatalf("FlatMap on Some(even): got (%d, %v)", v, ok)
	}

	odd := maybe.Some(3)
	if !maybe.FlatMap(odd, even).IsNone() {
		t.Fatal("FlatMap should produce None when f returns None")
	}

	if !maybe.FlatMap(maybe.None[int](), even).IsNone() {
		t.Fatal("FlatMap on None should be None")
	}
}

// --- JSON ---

func TestSomeMarshalsAsValue(t *testing.T) {
	data, err := json.Marshal(maybe.Some("hello"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `"hello"` {
		t.Fatalf("got %s, want \"hello\"", data)
	}
}

func TestNoneMarshalsAsNull(t *testing.T) {
	data, err := json.Marshal(maybe.None[string]())
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `null` {
		t.Fatalf("got %s, want null", data)
	}
}

func TestUnmarshalNullProducesNone(t *testing.T) {
	var m maybe.Maybe[int]
	if err := json.Unmarshal([]byte(`null`), &m); err != nil {
		t.Fatal(err)
	}
	if !m.IsNone() {
		t.Fatal("expected None")
	}
}

func TestUnmarshalValueProducesSome(t *testing.T) {
	var m maybe.Maybe[int]
	if err := json.Unmarshal([]byte(`42`), &m); err != nil {
		t.Fatal(err)
	}
	v, ok := m.Get()
	if !ok || v != 42 {
		t.Fatalf("got (%d, %v), want (42, true)", v, ok)
	}
}

func TestRoundTripStruct(t *testing.T) {
	type wrapper struct {
		Note maybe.Maybe[string] `json:"note"`
	}

	cases := []struct {
		name string
		in   wrapper
		want string
	}{
		{"some", wrapper{Note: maybe.Some("hi")}, `{"note":"hi"}`},
		{"none", wrapper{Note: maybe.None[string]()}, `{"note":null}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			data, err := json.Marshal(c.in)
			if err != nil {
				t.Fatal(err)
			}
			if string(data) != c.want {
				t.Fatalf("marshal: got %s, want %s", data, c.want)
			}

			var got wrapper
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatal(err)
			}
			if got.Note.IsSome() != c.in.Note.IsSome() {
				t.Fatalf("round-trip presence mismatch: got %v, want %v", got, c.in)
			}
			if v1, _ := got.Note.Get(); v1 != c.in.Note.OrElse("") {
				t.Fatalf("round-trip value mismatch: got %v, want %v", got, c.in)
			}
		})
	}
}
