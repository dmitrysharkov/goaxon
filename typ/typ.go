// Package typ collects small generic type primitives that are useful
// across the framework's app layer: Maybe[T] for optional values,
// Unit for "no meaningful return," and (later) more functor / list
// helpers.
//
// Each type is deliberately tiny, dependency-free, and Go-shaped.
// Combinators (Map, FlatMap, etc.) live as free functions because
// Go doesn't allow generic methods on non-generic types.
//
// Naming: "typ" rather than "type" because `type` is a reserved
// word in Go. A single short package name is intentional — it keeps
// `typ.Maybe[T]`, `typ.Unit`, `typ.Some(x)` readable at call sites.
package typ

// Unit is an alias for struct{}, conventionally used as a "no
// meaningful return" type — most often as the result type of a
// command that doesn't need to produce a value:
//
//	command.Register(bus, func(ctx, cmd PlaceOrder) (typ.Unit, error) {
//	    ...
//	    return typ.Unit{}, nil
//	})
//
// Because Unit is an *alias* (note the `=` in the declaration), any
// code already using struct{} keeps working — the alias is purely a
// readability cue. `typ.Unit{}`, `struct{}{}`, and even just calling
// it `var x struct{}` are all the same value of the same type.
type Unit = struct{}
