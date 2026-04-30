# goaxon — project context for Claude Code

A Go-idiomatic CQRS + event sourcing toolkit, inspired by Java's Axon Framework.
This file orients you (Claude) on the project's design decisions, conventions,
and roadmap. Read it fully before suggesting changes.

---

## Project shape

```
goaxon/
├── event/          Event, Envelope, Bus, Store interfaces; JSON Registry; Outbox interface; Dispatcher
├── aggregate/      Aggregate Root, embeddable Base, generic Repository[A]
├── command/        Type-safe command bus (generics-based)
├── query/          Type-safe query bus (generics-based)
├── store/
│   ├── memory/     In-memory Store and Bus implementations
│   └── postgres/   Postgres-backed Store with transactional outbox (pgx/v5)
├── internal/
│   └── pgtest/     Embedded-postgres + pgtestdb harness used by tests
└── examples/
    ├── orders/         In-process driver against the orders domain
    │   └── domain/     Shared orders core (commands, events, aggregate, projection, Wire)
    └── orders-http/    HTTP driver (chi) against the same domain
```

The two `examples/orders*` binaries deliberately share the same
`examples/orders/domain` package — they're hexagonal-style *driving
adapters* against one application core. New transport layers (gRPC,
queues, CLIs) belong as siblings, not as forks of the domain.

Module: `github.com/dmitrysharkov/goaxon` (placeholder; rename when publishing).
Go version: 1.26.

---

## Design decisions (and why)

### Generics over reflection for buses
Command and query buses are parameterised on the command/query type and result
type:

```go
command.Register[PlaceOrder, struct{}](bus, handler)
command.Send[PlaceOrder, struct{}](ctx, bus, cmd)
```

Axon (Java) uses runtime annotations and reflection. We deliberately don't —
generics give us compile-time type safety, no reflection in hot paths, and
better IDE support. The trade-off is slightly more verbose call sites; we
think that's worth it.

Do not introduce reflection-based handler discovery. If ergonomics suffer,
fix them with helper functions, not reflection.

### Value semantics for `event.Envelope`
We pass `Envelope` by value, not `*Envelope`. This was benchmarked:

| Scenario | Value | Pointer |
|----------|-------|---------|
| Reused envelope, 1 handler | ~55 ns/op, 0 allocs | ~21 ns/op, 0 allocs |
| Reused envelope, 10 handlers | ~58 ns/op, 0 allocs | ~39 ns/op, 0 allocs |
| **Construct + publish (realistic)** | **~155 ns/op, 1 alloc, 24 B** | **~272 ns/op, 2 allocs, 120 B** |

The realistic path — `Repository.Save` constructs a fresh envelope per event
— shows value wins by ~1.8x and uses ~5x less memory. Reason: pointer
envelopes escape to the heap (the compiler can't prove the pointer doesn't
outlive the function once it crosses the bus interface), so they trigger
heap allocation. Value envelopes stay on the stack.

The 1 remaining allocation in the value path is the `Event` interface boxing
(`OrderPlaced` -> `event.Event`), which happens regardless of envelope
representation.

If you're tempted to switch to `*Envelope` for performance, **don't** — it
makes things slower. If you want to optimise this further, the answer is
`sync.Pool`, not pointers.

### Aggregates are pointers, embed `*aggregate.Base`
Aggregates have mutable state, so they must use pointer receivers. They
embed `*aggregate.Base` for bookkeeping (uncommitted events, version):

```go
type Order struct {
    *aggregate.Base
    customer string
    amount   int
    status   orderStatus
}
```

The `Repository[A Root]` is generic over the concrete aggregate type, so
callers get `*Order` back without type assertions.

### Apply must be deterministic and side-effect-free
`Apply(event.Event)` runs both during normal operation and during replay.
It must not log, call external services, or do anything non-deterministic
— state mutation only. Treat it like a pure function from (state, event)
to state.

### Errors as values, no exception magic
Handlers return `error`. The bus does not retry implicitly. Retry policy is
the caller's choice and should be explicit. Same for dead-letter queues —
they're a deliberate piece of infrastructure, not a hidden default.

### `context.Context` everywhere
Every dispatch (`Send`, `Ask`, `Publish`) takes `context.Context` as the
first parameter. Cancellation, deadlines, and tracing flow through it.
Don't add APIs that omit it.

### JSON event registry for persistent stores
Persistent stores need to round-trip a domain event through bytes. We chose
JSON via a generic Registry rather than reflection-based discovery or a
wider codec interface:

```go
reg := event.NewRegistry()
event.Register[OrderPlaced](reg)
```

The in-memory store doesn't need a registry — it keeps Go values directly.
Only stores that serialize (Postgres, future Kafka, etc.) consult one.

If JSON becomes a real bottleneck (it usually doesn't), add a codec
parameter on the Registry rather than a second registry type.

### Transactional outbox via interface assertion
A persistent store that wants crash-safe dispatch implements both
`event.Store` and `event.Outbox`. The Repository type-asserts at runtime:
if the store is also an `Outbox`, `Save` writes events + outbox in one
transaction and returns; the synchronous `bus.Publish` loop is skipped.
An out-of-band `event.Dispatcher` reads the outbox and publishes.

This keeps the in-memory store untouched (still synchronous publish) and
avoids growing `Repository`'s constructor with a "publisher strategy" knob.

`event.Outbox` exposes a single `Claim(ctx, batchSize)` method returning
an `event.Claim`. Claim-based instead of plain `LoadPending`/`MarkDispatched`
because the lock has to live across `load → publish → mark`: the Postgres
implementation runs `SELECT … FOR UPDATE SKIP LOCKED` inside a long-lived
transaction that `Claim.Commit` (UPDATE + commit) or `Claim.Release`
(rollback) closes out. As a result **multiple dispatchers can run safely
against the same outbox** — concurrent claims see disjoint sets of rows,
and a crashed dispatcher's locks are released by Postgres when its
connection drops. See [NOTES.md](NOTES.md) for the lease-via-`claimed_at`
alternative we considered and why we didn't pick it.

Delivery is at-least-once: a Publish that succeeds but a Commit
that subsequently fails (or a process crash between the two) will
redeliver. The framework's "handlers must be idempotent" rule absorbs
that — see Conventions.

The Dispatcher stops a batch on the first publish error to preserve
ordering for projections; the failing entry stays pending (the claim
is Released without marking) and is retried on the next tick. There
is no built-in DLQ — surface failures via `WithErrorHandler` and
decide policy yourself.

### Aggregate IDs are `uuid.UUID`
`event.Envelope.AggregateID`, `aggregate.Root.AggregateID()`, and the
factories passed to `Repository` are all typed `uuid.UUID` (from
`github.com/google/uuid`). UUIDv7 is recommended at the application
level — `uuid.NewV7()` — because the time-ordered prefix gives the
events table good B-tree locality. The framework doesn't enforce v7;
v4 or any valid UUID will work.

The Postgres `aggregate_id` column is `uuid`, not `text`. We pass values
to pgx as `[16]byte(id)` (and scan back the same way) since pgx/v5
recognizes `[16]byte` for the uuid type without extra codec
registration.

### Optimistic concurrency in Postgres
The events table has `PRIMARY KEY (aggregate_id, sequence)`. `Append`
checks the head sequence inside its tx and converts unique-constraint
violations to `event.ErrConcurrencyConflict`. Both paths fire on real
concurrent appenders; together they make the contract identical to the
in-memory store's.

### No memory arenas
We considered them. They don't fit:
- Events live ~forever (in the store, in projections), so they can't share an
  arena lifetime.
- The hot path's bottleneck is interface boxing, not envelope allocation;
  arenas don't help with that.
- Production CQRS systems are dominated by I/O time, not allocation time.
- The standard library `arena` package is on hold indefinitely;
  third-party libraries (nuke, wundergraph/go-arena) have safety concerns.

If allocation pressure becomes a real problem (measured, not assumed), reach
for `sync.Pool` first.

---

## Conventions

- **Event names: past tense.** `OrderPlaced`, not `PlaceOrder`. Past tense =
  event, imperative = command. This is a hard naming rule.
- **One handler per command/query type.** Multiple is a bug; the bus will
  panic on duplicate registration.
- **Multiple handlers per event type are fine** and expected (one per
  projection).
- **Event handlers must be idempotent.** Events may be redelivered on retry
  or replay. A handler that increments a counter without a dedupe key is
  broken.
- **Events stored in a persistent Store must be registered** via
  `event.Register[T](registry)` before `Load` is called on that store. The
  in-memory store doesn't need this; Postgres (and future serializing
  stores) do.
- **Tests that need Postgres use `internal/pgtest`.** `pgtest.Run(m)` from
  `TestMain` boots an embedded PG; `pgtest.NewPool(t, ddl)` returns a
  fresh, isolated database per test (template-clone via pgtestdb).
  Multiple test binaries running in parallel each get their own
  RuntimePath and free port, so `go test ./...` is safe.

---

## Known limitations of v1 (intentional, not bugs)

These are documented gaps, not things to fix without discussion:

1. **In-memory bus has no transactional outbox.** `Repository.Save` against
   a plain `event.Store` (the in-memory one) appends and then publishes
   non-atomically. The whole thing is in-process so a crash takes
   everything down anyway. The Postgres store implements `event.Outbox`
   and gets crash-safe dispatch via the Dispatcher.
2. **Synchronous event bus.** `memory.Bus` delivers handlers serially in
   `Publish` — a slow projection blocks the publisher. With the Postgres
   store this is mitigated because the Dispatcher publishes off the
   command path, but the bus implementation itself is still synchronous.
   An async Bus is on the roadmap.
3. **No snapshotting.** Long event streams replay slowly. Will be added
   with a `Snapshotter` interface.
4. **No event upcasters.** Renaming an event type or changing its payload
   shape is currently a breaking change to the event log.

---

## Roadmap (in rough priority order)

- [x] Postgres event store with transactional outbox
- [x] UUIDv7 aggregate IDs (typed `uuid.UUID` at the framework boundary)
- [x] Multi-dispatcher safety (`SELECT … FOR UPDATE SKIP LOCKED` via `event.Outbox.Claim`)
- [ ] Async event bus with retries and DLQ
- [ ] Snapshotting (every N events, restore from latest)
- [ ] Sagas / process managers for cross-aggregate workflows
- [ ] Event upcasters for schema evolution
- [ ] gRPC layer for remote command/query dispatch

---

## When suggesting changes

- **Benchmark before optimising.** The codebase already has surprising
  performance characteristics (see the value/pointer table above). Run
  `go test -bench=. -benchmem` and check the numbers, don't guess.
- **Run `go build -gcflags="-m"`** to verify escape behaviour for any
  change in the hot path (bus dispatch, repository save).
- **Preserve the public API shape.** Generic functions and interface names
  are the framework's contract. Renaming `Repository.Save` to
  `Repository.Commit` is a breaking change, even if you think it reads
  better.
- **Update this file** when a design decision changes. Fresh sessions
  read this for context; stale guidance here is worse than none.
- **Prefer small, reviewable changes.** This is a framework — surface area
  matters. Splitting a 500-line PR into five 100-line PRs is almost
  always worth it.

---

## Useful commands

```bash
# Run the example
cd examples/orders && go run .

# Run all tests (the postgres ones boot embedded PG — first run downloads
# the binary into ~/.cache/goaxon-pgtest, subsequent runs are quick)
go test ./...

# Run only the in-process tests (no embedded PG)
go test ./event/... ./aggregate/... ./command/... ./query/... ./store/memory/...

# Run all benchmarks (when they exist)
go test -bench=. -benchmem ./...

# Check escape analysis (very useful for this project)
go build -gcflags="-m" ./...

# More verbose escape analysis with reasoning
go build -gcflags="-m -m" ./...
```
