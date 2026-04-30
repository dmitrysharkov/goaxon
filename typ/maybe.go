// This file holds Maybe[T] — the generic optional-value type from
// typed-functional languages, adapted to Go. The package-level doc
// is in typ.go.
//
// A Maybe[T] either contains a value (Some) or is empty (None). It's
// useful for fields that are genuinely optional in the domain, where
// `*T` would technically work but conflates "not set" with the
// pointer-aliasing semantics most fields don't want, and where the
// zero value of T isn't a meaningful "absent" marker (e.g. an empty
// string is invalid for a non-empty value object — there's no way to
// represent "absent" with the value alone).
//
//	notes := typ.Some(notesVO)         // present
//	none  := typ.None[Notes]()         // absent
//	if v, ok := notes.Get(); ok { ... } // standard Go idiom
//
// Wire format: Maybe[T] marshals to JSON as either the marshaled T
// (Some) or null (None). It unmarshals symmetrically.
package typ

import (
	"bytes"
	"encoding/json"
	"errors"
)

// Maybe[T] is either a present value (Some) or absent (None).
type Maybe[T any] struct {
	value   T
	present bool
}

// Some wraps a present value.
func Some[T any](v T) Maybe[T] { return Maybe[T]{value: v, present: true} }

// None returns an empty Maybe[T]. The zero value of Maybe[T] is also
// a valid None.
func None[T any]() Maybe[T] { return Maybe[T]{} }

// Get returns the value and a present-flag, mirroring Go's standard
// "comma-ok" idiom.
func (m Maybe[T]) Get() (T, bool) { return m.value, m.present }

// IsSome reports whether the Maybe contains a value.
func (m Maybe[T]) IsSome() bool { return m.present }

// IsNone reports whether the Maybe is empty.
func (m Maybe[T]) IsNone() bool { return !m.present }

// OrElse returns the contained value if Some, otherwise fallback.
func (m Maybe[T]) OrElse(fallback T) T {
	if m.present {
		return m.value
	}
	return fallback
}

// MustGet returns the value or panics if None. Use Get instead unless
// you can prove the Maybe is Some at the call site.
func (m Maybe[T]) MustGet() T {
	if !m.present {
		panic("maybe: MustGet on None")
	}
	return m.value
}

// Map applies f to the contained value (if any) and returns a Maybe
// of the result. None propagates: Map on None is None.
//
// This is the functor "fmap": Map[A,B](Some(a), f) = Some(f(a)),
// Map[A,B](None, f) = None.
func Map[A, B any](m Maybe[A], f func(A) B) Maybe[B] {
	if !m.present {
		return Maybe[B]{}
	}
	return Maybe[B]{value: f(m.value), present: true}
}

// FlatMap (the monadic bind) applies f to the contained value and
// returns f's result. Useful when f itself can fail/be-absent.
func FlatMap[A, B any](m Maybe[A], f func(A) Maybe[B]) Maybe[B] {
	if !m.present {
		return Maybe[B]{}
	}
	return f(m.value)
}

// MarshalJSON renders Some(v) as the JSON of v, and None as null.
func (m Maybe[T]) MarshalJSON() ([]byte, error) {
	if !m.present {
		return []byte("null"), nil
	}
	return json.Marshal(m.value)
}

// UnmarshalJSON parses null as None and any other JSON value as
// Some(v).
func (m *Maybe[T]) UnmarshalJSON(data []byte) error {
	if data == nil {
		return errors.New("maybe: nil input")
	}
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		*m = Maybe[T]{}
		return nil
	}
	var v T
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	m.value = v
	m.present = true
	return nil
}
