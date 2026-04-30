# goaxon — project context for Claude Code

A Go-idiomatic CQRS + event sourcing toolkit, inspired by Java's Axon Framework.
This file orients you (Claude) on the project's design decisions, conventions,
and roadmap. Read it fully before suggesting changes.

---

## Project shape

```
goaxon/
├── event/          Event, Envelope, Bus, Store interfaces (the foundation)
├── aggregate/      Aggregate Root, embeddable Base, generic Repository[A]
├── command/        Type-safe command bus (generics-based)
├── query/          Type-safe query bus (generics-based)
├── store/
│   └── memory/     In-memory Store and Bus implementations
└── examples/
    └── orders/     End-to-end demo: place + ship an order, project a read model
```

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
- **No `_test.go` files yet.** When we add them, prefer table-driven tests
  and use `testify/require` (not `assert`) for fail-fast behaviour.

---

## Known limitations of v1 (intentional, not bugs)

These are documented gaps, not things to fix without discussion:

1. **No transactional outbox.** `Repository.Save` appends to the store, then
   publishes to the bus — non-atomic. With the in-memory bus this is fine.
   Adding a Postgres store will require an outbox.
2. **Synchronous event bus.** A slow projection blocks command throughput.
   Acceptable for v1 and tests; needs to become async for production.
3. **No snapshotting.** Long event streams replay slowly. Will be added
   with a `Snapshotter` interface.
4. **No event upcasters.** Renaming an event type or changing its payload
   shape is currently a breaking change to the event log.
5. **In-memory store only.** The `event.Store` interface is designed for
   pluggable backends; Postgres is the next target.

---

## Roadmap (in rough priority order)

- [ ] Async event bus with retries and DLQ
- [ ] Postgres event store with transactional outbox
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

# Run all benchmarks (when they exist)
go test -bench=. -benchmem ./...

# Check escape analysis (very useful for this project)
go build -gcflags="-m" ./...

# More verbose escape analysis with reasoning
go build -gcflags="-m -m" ./...
```
