# goaxon

A Go-idiomatic CQRS + event sourcing toolkit, inspired by Java's Axon
Framework.

## Status

Working v1. In-memory and Postgres event stores; transactional outbox
with multi-dispatcher safety (`SELECT … FOR UPDATE SKIP LOCKED`); typed
generic command/query buses; UUIDv7 aggregate IDs. The `orders` example
domain is wired to two driving adapters: an in-process driver and an
HTTP one (chi).

Public surface stays small enough to fit in your head.
[CLAUDE.md](CLAUDE.md) has the design rationale and trade-offs;
[NOTES.md](NOTES.md) covers alternatives we considered (e.g. lease-based
outbox vs the long-running-tx model that won).

## What's here

```
goaxon/
├── event/             Event, Envelope, Bus, Store; JSON Registry; Outbox + Claim; Dispatcher
├── aggregate/         Aggregate Root, Base helper, generic Repository[A]
│   └── aggregatetest/ Given/When/Then black-box harness for aggregate tests
├── command/           Type-safe command bus (generics, no reflection)
├── query/             Type-safe query bus
├── validation/        Generic Validator + Error for parse-don't-validate at app boundary
├── store/
│   ├── memory/        In-memory Store and Bus
│   └── postgres/      Postgres Store with transactional outbox (pgx/v5)
├── internal/
│   └── pgtest/        Embedded-postgres + pgtestdb harness for tests
└── examples/
    ├── orders/        In-process driver (calls the app service)
    │   ├── domain/    Aggregate, commands, events, projection (the core)
    │   └── app/       Typed application service (the use-case layer)
    └── orders-http/   HTTP driver (chi) against the same app + domain
```

The orders example demonstrates a three-layer hexagonal split:
the **domain** owns the aggregate and the bus handlers, the **app**
package is a typed facade (`PlaceOrder(ctx, customer, amount)`) that
dispatches through the command/query bus, and **adapters**
(`orders/main.go`, `orders-http/main.go`) call only the app service —
they don't import `command`, `query`, or `event` directly.

## Try it

```bash
# In-process demo: place + ship an order, query the read model
go run ./examples/orders
# -> Order summary: {OrderID:019dde50-... Customer:Alice Amount:4200 Shipped:true}

# HTTP demo: same domain over chi on :8080
go run ./examples/orders-http

# In another shell:
curl -s -X POST localhost:8080/orders \
     -H 'Content-Type: application/json' \
     -d '{"customer":"Alice","amount":4200}'
# -> {"order_id":"019dde..."}

curl -X POST localhost:8080/orders/<order_id>/ship   # 204
curl localhost:8080/orders/<order_id>                # 200 + summary

# Full test suite (downloads & boots an embedded Postgres on first run)
go test ./...
```

## Design choices

- **Generics over annotations.** `command.Register[PlaceOrder, struct{}](bus, h)` — compile-time type safety, no reflection on the dispatch hot path.
- **Value semantics for `event.Envelope`.** Benchmarked decision; pointer-passing escapes to the heap and roughly doubles allocations on the realistic save path.
- **Transactional outbox via interface assertion.** Stores that also implement `event.Outbox` (the Postgres one does) cause `Repository.Save` to skip its synchronous publish loop and rely on `event.Dispatcher` instead — crash-safe by construction.
- **Claim-based dispatch with `FOR UPDATE SKIP LOCKED`.** Multiple dispatcher processes can run safely against the same outbox; concurrent claims see disjoint sets of rows. Crashed dispatchers lose their locks automatically when Postgres detects the connection drop.
- **At-least-once delivery; handlers must be idempotent.** No implicit retries, no built-in DLQ — those are deliberate infrastructure decisions, not hidden defaults.
- **UUIDv7 aggregate IDs.** Time-ordered prefix gives the events table good B-tree locality.
- **Hexagonal-friendly examples.** The orders aggregate is one core; the in-process and HTTP demos are two driving adapters against it. Adding gRPC, a CLI, or a queue consumer is a sibling directory, not a fork.
- **Application-layer facade in the example.** `examples/orders/app` exposes typed methods (`PlaceOrder`, `ShipOrder`, `GetOrder`) that adapters call instead of touching the command/query bus directly. The bus stays as the dispatch mechanism beneath; the app layer just gives adapters a stable API and centralises error mapping (`event.ErrStreamNotFound` and `domain.ErrNotFound` → `app.ErrNotFound`).
- **Parse-don't-validate at the app boundary.** Aggregate methods take value objects (`Customer`, `Amount`) — invalid values are not representable. Adapters pass raw primitives (string, int) into the app layer; the app layer parses and accumulates failures via `goaxon/validation`, returning `*validation.Error` which adapters render as 422 / structured field errors.
- **Black-box aggregate tests.** `aggregate/aggregatetest` provides a Given/When/Then harness using only the aggregate's public surface — no internals of `Base` are exposed to support testing.
- **`context.Context` everywhere.** Cancellation, deadlines, and tracing flow through every dispatch.

## Known limitations

These are intentional gaps, called out so they don't surprise you:

- **In-memory event bus is synchronous** — a slow projection blocks the publisher. With the Postgres store this is mitigated because the dispatcher publishes off the command path, but the bus itself is still serial.
- **No snapshotting yet** — replay walks the full stream. Fine for shorter aggregates; will hurt long-lived ones.
- **No event upcasters** — renaming an event type or changing its payload shape is currently a breaking change to the event log.

## Roadmap

- [x] Postgres event store with transactional outbox
- [x] UUIDv7 aggregate IDs
- [x] Multi-dispatcher safety (`FOR UPDATE SKIP LOCKED` via `event.Outbox.Claim`)
- [ ] Async in-memory event bus with retries and DLQ
- [ ] Snapshotting (every N events, restore from latest)
- [ ] Sagas / process managers for cross-aggregate workflows
- [ ] Event upcasters for schema evolution
- [ ] gRPC layer for remote command/query dispatch
