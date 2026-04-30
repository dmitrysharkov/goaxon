package event

import "errors"

// ErrConcurrencyConflict is returned by Store.Append when the expected
// version doesn't match the stream's actual head. The caller should
// reload the aggregate and retry the command.
var ErrConcurrencyConflict = errors.New("event: concurrency conflict")

// ErrStreamNotFound is returned by Store.Load when no events exist
// for the given aggregate ID.
var ErrStreamNotFound = errors.New("event: stream not found")
