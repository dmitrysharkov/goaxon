// Package validation provides a small, generic helper for accumulating
// per-field parse errors at the application/use-case boundary.
//
// The pattern is "parse, don't validate": adapters hand the app layer
// raw inputs (string, int, etc.); the app layer parses each into the
// corresponding domain value object via a Parse* / Make*From* function;
// any failures are accumulated by field name into a *validation.Error.
//
//	v := validation.New()
//	cust := validation.Field(v, "customer", domain.ParseCustomer, customer)
//	amt  := validation.Field(v, "amount",   domain.MakeAmountFromCents, amount)
//	if err := v.Err(); err != nil {
//	    return "", err // *validation.Error — adapters can errors.As
//	}
//
// The package is intentionally tiny and dependency-free. It doesn't
// know about CQRS, the framework's buses, or HTTP — just about
// "run this parser, remember which field failed."
package validation

import (
	"fmt"
	"sort"
	"strings"
)

// Error is returned when one or more inputs fail to parse. Fields maps
// field name to the parse-error message; the type is map[string]string
// (rather than map[string]error) so it round-trips through encoding/json
// without ceremony, which is what most adapters want.
type Error struct {
	Fields map[string]string
}

func (e *Error) Error() string {
	parts := make([]string, 0, len(e.Fields))
	for f, msg := range e.Fields {
		parts = append(parts, fmt.Sprintf("%s: %s", f, msg))
	}
	sort.Strings(parts) // deterministic output for tests
	return "validation: " + strings.Join(parts, "; ")
}

// Validator accumulates per-field parse errors. Use Field to feed
// inputs through their parsers; Err returns nil or a *Error at the
// end. A Validator is single-use (intended for one app-layer call);
// don't reuse one across requests.
type Validator struct {
	errs map[string]string
}

// New returns an empty Validator.
func New() *Validator { return &Validator{errs: map[string]string{}} }

// Field runs parser(input). If the parser errors, the message is
// recorded against name and the zero value of Out is returned. The
// caller can keep using the result and just check v.Err() at the end:
// the zero value won't be propagated past the v.Err() check, since
// any failure makes Err() non-nil.
//
// Type parameters: In is the raw input type (string, int, etc.);
// Out is the parsed type (typically a domain value object). Both
// are inferred from the parser argument, so call sites stay terse.
func Field[In, Out any](v *Validator, name string, parser func(In) (Out, error), input In) Out {
	out, err := parser(input)
	if err != nil {
		v.errs[name] = err.Error()
	}
	return out
}

// Err returns nil if every Field call succeeded, or a *Error
// aggregating all the failures.
func (v *Validator) Err() error {
	if len(v.errs) == 0 {
		return nil
	}
	return &Error{Fields: v.errs}
}
