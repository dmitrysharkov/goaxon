package validation_test

import (
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/dmitrysharkov/goaxon/validation"
)

// Compile-time check: *Error satisfies the error interface.
var _ error = (*validation.Error)(nil)

func TestEmptyValidatorErrIsNil(t *testing.T) {
	v := validation.New()
	if err := v.Err(); err != nil {
		t.Fatalf("got %v, want nil", err)
	}
}

func TestFieldWithSuccessfulParse(t *testing.T) {
	v := validation.New()
	got := validation.Field(v, "n", strconv.Atoi, "42")
	if got != 42 {
		t.Fatalf("got %d, want 42", got)
	}
	if err := v.Err(); err != nil {
		t.Fatalf("expected nil err, got %v", err)
	}
}

func TestFieldWithFailedParseReturnsZero(t *testing.T) {
	v := validation.New()
	got := validation.Field(v, "n", strconv.Atoi, "not-an-int")
	if got != 0 {
		t.Fatalf("got %d, want zero on parse failure", got)
	}
	err := v.Err()
	if err == nil {
		t.Fatal("expected error")
	}

	var verr *validation.Error
	if !errors.As(err, &verr) {
		t.Fatalf("got %T, want *validation.Error", err)
	}
	if _, ok := verr.Fields["n"]; !ok {
		t.Fatalf("missing 'n' field: %+v", verr.Fields)
	}
}

func TestMultipleFieldFailuresAccumulate(t *testing.T) {
	v := validation.New()
	validation.Field(v, "a", strconv.Atoi, "x")
	validation.Field(v, "b", strconv.Atoi, "y")

	var verr *validation.Error
	if !errors.As(v.Err(), &verr) {
		t.Fatalf("expected *validation.Error, got %v", v.Err())
	}
	if len(verr.Fields) != 2 {
		t.Fatalf("got %d fields, want 2: %+v", len(verr.Fields), verr.Fields)
	}
}

func TestErrorMessageIsDeterministicAndIncludesFields(t *testing.T) {
	v := validation.New()
	validation.Field(v, "z", strconv.Atoi, "x")
	validation.Field(v, "a", strconv.Atoi, "x")

	msg := v.Err().Error()
	if !strings.Contains(msg, "a:") || !strings.Contains(msg, "z:") {
		t.Fatalf("message missing field names: %q", msg)
	}
	// Sorted output: 'a' must appear before 'z'.
	if strings.Index(msg, "a:") > strings.Index(msg, "z:") {
		t.Fatalf("expected sorted field order in %q", msg)
	}
}

// parseEven succeeds only on even numbers — exercises the type
// parameters with a non-stdlib parser.
func parseEven(n int) (int, error) {
	if n%2 != 0 {
		return 0, errors.New("must be even")
	}
	return n, nil
}

func TestFieldGenericsInferInputAndOutput(t *testing.T) {
	v := validation.New()
	got := validation.Field(v, "n", parseEven, 4)
	if got != 4 {
		t.Fatalf("got %d, want 4", got)
	}
	if err := v.Err(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	v = validation.New()
	validation.Field(v, "n", parseEven, 3)
	if v.Err() == nil {
		t.Fatal("expected error on odd input")
	}
}
