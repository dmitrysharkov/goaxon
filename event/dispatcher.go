package event

import (
	"context"
	"fmt"
	"time"
)

// Dispatcher consumes an Outbox and publishes its entries to a Bus.
// Run it as a goroutine alongside your application:
//
//	disp := event.NewDispatcher(store, bus)
//	go disp.Run(ctx)
//
// On every tick the dispatcher loads up to BatchSize pending entries,
// publishes them in order, then marks the successful ones dispatched.
// Delivery is at-least-once: a Publish that succeeds but a
// MarkDispatched that subsequently fails (or a process crash between
// the two) will redeliver. Handlers must be idempotent — see
// CLAUDE.md.
//
// The dispatcher stops a batch at the first Publish error so later
// entries don't overtake a stalled one (ordering matters for
// projections). The failing entry stays pending and is retried on the
// next tick. There is no built-in dead-letter queue; if you need one,
// observe failures via WithErrorHandler and act on them.
type Dispatcher struct {
	outbox       Outbox
	bus          Bus
	pollInterval time.Duration
	batchSize    int
	onError      func(context.Context, error)
}

// DispatcherOption configures a Dispatcher.
type DispatcherOption func(*Dispatcher)

// WithPollInterval sets how long the dispatcher waits between polls
// when the outbox was empty (or nearly empty). Defaults to 100ms.
func WithPollInterval(d time.Duration) DispatcherOption {
	return func(disp *Dispatcher) { disp.pollInterval = d }
}

// WithBatchSize sets the maximum number of entries fetched per poll.
// Defaults to 100.
func WithBatchSize(n int) DispatcherOption {
	return func(disp *Dispatcher) { disp.batchSize = n }
}

// WithErrorHandler installs a callback invoked for every non-fatal
// error: failed loads, failed publishes, failed marks. Errors are
// wrapped with enough context to be useful in logs. The default is a
// no-op — surface them however your app prefers (slog, metrics, etc.).
func WithErrorHandler(fn func(context.Context, error)) DispatcherOption {
	return func(disp *Dispatcher) { disp.onError = fn }
}

// NewDispatcher returns a Dispatcher with the given Outbox source and
// Bus sink, configured by opts.
func NewDispatcher(outbox Outbox, bus Bus, opts ...DispatcherOption) *Dispatcher {
	d := &Dispatcher{
		outbox:       outbox,
		bus:          bus,
		pollInterval: 100 * time.Millisecond,
		batchSize:    100,
		onError:      func(context.Context, error) {},
	}
	for _, opt := range opts {
		opt(d)
	}
	return d
}

// Run blocks until ctx is cancelled. Returns ctx.Err() on shutdown.
func (d *Dispatcher) Run(ctx context.Context) error {
	timer := time.NewTimer(0) // fire immediately on first iteration
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
		}
		fetched := d.tick(ctx)
		// If we filled the batch, the queue probably has more — keep
		// draining without sleeping.
		wait := d.pollInterval
		if fetched == d.batchSize {
			wait = 0
		}
		timer.Reset(wait)
	}
}

// tick processes one batch and returns the number of entries fetched
// (regardless of whether each publish succeeded). Returning zero on
// errors lets Run back off rather than tight-loop on a broken DB.
func (d *Dispatcher) tick(ctx context.Context) int {
	entries, err := d.outbox.LoadPending(ctx, d.batchSize)
	if err != nil {
		d.onError(ctx, fmt.Errorf("dispatcher: load pending: %w", err))
		return 0
	}
	if len(entries) == 0 {
		return 0
	}

	delivered := make([]int64, 0, len(entries))
	for _, entry := range entries {
		if err := d.bus.Publish(ctx, entry.Envelope); err != nil {
			d.onError(ctx, fmt.Errorf("dispatcher: publish %s outbox_id=%d: %w",
				entry.Envelope.Payload.EventType(), entry.OutboxID, err))
			break // preserve ordering — stop the batch on first failure
		}
		delivered = append(delivered, entry.OutboxID)
	}

	if len(delivered) > 0 {
		if err := d.outbox.MarkDispatched(ctx, delivered); err != nil {
			d.onError(ctx, fmt.Errorf("dispatcher: mark dispatched: %w", err))
			// Don't return — the publishes already happened. Next tick
			// will redeliver these entries; idempotent handlers absorb it.
		}
	}
	return len(entries)
}
