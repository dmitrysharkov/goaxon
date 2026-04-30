# goaxon — design notes

Things worth remembering that aren't quite a design *decision* (those live
in CLAUDE.md) and aren't quite a roadmap item (those are in CLAUDE.md too)
— alternatives we considered, knobs we left at their defaults, etc.

---

## Alternative outbox concurrency model: lease via `claimed_at`

We picked the long-running-tx + `FOR UPDATE SKIP LOCKED` approach for
multi-dispatcher safety (see `event.Outbox.Claim`). The alternative we
seriously considered was a **lease-based** scheme. Here's enough detail
to switch later if the trade-offs flip.

### The idea

Add a `claimed_at timestamptz` column to the outbox. A dispatcher claims
work atomically via a single SQL statement that combines `SELECT … FOR
UPDATE SKIP LOCKED` with an `UPDATE` to mark `claimed_at`:

```sql
WITH candidate AS (
    SELECT id FROM outbox
    WHERE dispatched_at IS NULL
      AND (claimed_at IS NULL OR claimed_at < now() - $2::interval)
    ORDER BY id
    LIMIT $1
    FOR UPDATE SKIP LOCKED
)
UPDATE outbox
SET    claimed_at = now()
WHERE  id IN (SELECT id FROM candidate)
RETURNING id, aggregate_id, …;
```

The transaction commits *immediately*. The "lock" is held by the
`claimed_at` value rather than a database lock — other dispatchers see
that the row was claimed recently and skip it. After publishing, the
dispatcher sets `dispatched_at`. If it crashes mid-publish, after
`lease_duration` the row becomes claimable again.

### Why we didn't pick it

| Concern | Long-running tx (chosen) | Lease |
|---|---|---|
| Schema | one column (`dispatched_at`) | two (`dispatched_at`, `claimed_at`) |
| Connection cost | one held during publish phase | none (tx commits immediately) |
| Dead-dispatcher recovery | instant (PG kills the tx, lock released) | wait for lease expiry |
| Failed-publish retry | next tick (claim is rolled back) | wait for lease expiry, OR explicit "release" UPDATE |
| Code complexity | three small methods | CTE + lease config + clock skew handling |
| Operator visibility | lock is invisible in normal SQL views | `claimed_at` is queryable |

The deciding factor was complexity. The long-running-tx model is the
canonical Postgres pattern for this and our batch-size × per-publish
latency budget keeps the connection-hold cost negligible. Lease pays
off when:

- you want **zero** open transactions on the events DB during publish
  (e.g., tiny pgbouncer pool, or publishes that block on slow external
  systems and you don't want to tie up a Postgres connection per
  dispatcher), or
- you want operator visibility into "what's currently being processed"
  via plain SQL.

Neither has bitten us. If either does, the migration path is:

1. Add `claimed_at timestamptz` column (nullable, no default).
2. Replace `Store.Claim`'s implementation with the CTE above; commit
   the transaction inside `Claim` rather than holding it.
3. Add a `Release` UPDATE that clears `claimed_at` for the listed IDs
   (so a publish-failure doesn't wait for lease expiry).
4. Keep the `event.Claim` interface unchanged — only the Postgres
   implementation differs. Other backends (Kafka, NATS) will likely
   want their own model again.

### Things to think about if you do switch

- **Lease duration** is a config knob. Too short → premature retries
  while slow handlers are still publishing. Too long → recovery from
  crashes is slow. Default: ~5× p99 publish latency.
- **Clock skew** between the DB and dispatchers matters because
  `claimed_at < now() - lease` is evaluated on the DB clock but the
  publish-attempt deadline is local. Use the DB clock for both and
  you're fine; mixing is a footgun.
- **Idempotent handlers** are still required. After a lease expires,
  another dispatcher can re-publish events the original dispatcher
  was already most-of-the-way through. Same at-least-once contract
  as today.

---
