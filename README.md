# goaxon

A Go-idiomatic CQRS + event sourcing toolkit, inspired by Java's Axon Framework.

## Status

v1 skeleton — single-process, in-memory store. The interfaces are designed
to be replaced (Postgres store, NATS bus) without touching aggregates or
handlers.

## What's here

```
goaxon/
├── event/      Event, Envelope, Bus, Store interfaces
├── aggregate/  Aggregate root, Base helper, generic Repository
├── command/    Type-safe command bus (generics, no reflection)
├── query/      Type-safe query bus
├── store/
│   └── memory/ In-memory Store and Bus
└── examples/
    └── orders/ End-to-end demo: place + ship an order, project a read model
```

## Run the example

```bash
cd examples/orders
go run .
```

Expected output:

```
Order summary: {OrderID:ord-1 Customer:Alice Amount:4200 Shipped:true}
Stored 2 events:
  seq=1 type=OrderPlaced
  seq=2 type=OrderShipped
```

## Design choices vs Axon

- **Generics over annotations.** `command.Register[PlaceOrder, struct{}](bus, h)` — compile-time type safety, no reflection at dispatch time.
- **Interfaces over magic.** Aggregates implement `Apply(event.Event)`; no `@AggregateRoot` annotation.
- **Errors as values.** Handlers return `error`; the bus applies whatever retry policy the caller wires up. No exception magic.
- **`context.Context` everywhere.** Cancellation, deadlines, and tracing for free.
- **No transactional outbox in v1.** Append-then-publish is best-effort; a crash between the two loses the publish. The example bus is synchronous, so in-process this is fine, but a Postgres store should add an outbox.

## Roadmap

- [ ] Snapshotting (avoid replaying long streams)
- [ ] Postgres event store
- [ ] Sagas / process managers
- [ ] Upcasters for event schema evolution
- [ ] Async event bus with retries and DLQ
```
