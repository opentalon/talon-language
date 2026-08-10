# Thresholds (inline & cached)

Talon has two ways to put a *learned number* into a rule — a threshold the
data decides, not a magic constant hard-coded by hand.

## Two forms

**Inline (compute-on-demand)** — `learned_threshold`:

```talon
detect "Slow request" {
  for records where attr "latency" > learned_threshold p95 of attr "latency" over last 30 days
  flag matching items
}
```

Every evaluation walks the matched-row series and computes the percentile.
Simple, always current, no infrastructure — but it pays that cost on each run.

**Cached (precomputed)** — the `threshold` block:

```talon
threshold "service_interval" {
  value 18200
  computed_from "47 service tickets, avg 20222 km, margin 0.9"
  valid_until "2025-05-13"
}

detect "Service overdue" {
  for records where type == "item"
    and attr "km" > attr "last_service_km" + threshold "service_interval"
  flag matching items
}
```

The host's discovery job runs the expensive computation on a schedule, writes
the answer into a generated `.tln` file, and the runtime just *looks it up*.
A `threshold "name"` reference resolves to the cached value — one lookup, no
per-eval series walk.

| Form | Cost per eval | When it fits |
|---|---|---|
| `learned_threshold …` (inline) | walks the series each eval | small data, fast aggregates, no caching layer |
| `threshold "name"` (cached) | one lookup | large history, expensive computation, host already runs discovery on a schedule |

## The `threshold` block

```
threshold "<name>" {
  value <number>              // required — what `threshold "<name>"` resolves to
  computed_from "<text>"      // optional — provenance, surfaced for audit
  valid_until "<date>"        // optional — YYYY-MM-DD or RFC 3339 expiry
}
```

A `threshold "name"` reference is an ordinary numeric expression: it composes
in arithmetic and comparisons anywhere a number can go — `attr "x" > threshold
"t"`, `attr "a" + threshold "t"`, and so on.

## Resolution

Threshold references are **inlined at plan time**: the planner replaces each
`threshold "name"` with the cached block's `value` before the query is built,
so the rest of the pipeline only ever sees a plain number. The block itself
produces no query — it's data, not an evaluable rule.

The host can regenerate the `.tln` file with a new `value` and the next
compile picks it up — no code change, no redeploy of Talon itself.

## Expiry

`valid_until` is an expiry hint for the host's refresh job, not a hard gate.
When it passes:

- the **validator warns** (`threshold "x" expired on … — its stale value is
  still used; the host discovery job should refresh it`), and
- the **executor keeps using the stale value**.

This is deliberate: a slightly stale threshold is almost always better than a
crashed rule. Keeping the value live puts the responsibility where it belongs
— on the host's scheduled discovery job to refresh before `valid_until` hits —
while the warning makes the staleness visible in `talon build` output.

The validator also rejects a `threshold "name"` reference with no matching
block, and a `valid_until` that isn't a date.

## Worked example

Files: [`examples/cached_threshold.tln`](../examples/cached_threshold.tln)
and [`test/cached_threshold.tln.test`](../test/cached_threshold.tln.test).

The cached interval is `18200`, so an item is overdue when
`km > last_service_km + 18200`:

| item | km | last_service_km | 43200 cutoff | outcome |
|---|---|---|---|---|
| Truck A | 45000 | 25000 | over | **flagged** |
| Van B | 40000 | 25000 | under | not flagged |

```
./talon build   examples/cached_threshold.tln
./talon test    examples/cached_threshold.tln test/cached_threshold.tln.test
./talon explain examples/cached_threshold.tln test/cached_threshold.tln.test
```

```
ACTION    Truck A: 45000 km — over the 25000 km + cached interval
ITEM      Truck A  (entity #1)
```

## Notes

- `value` is matched contextually inside the block, not reserved globally, so
  it stays usable as an ordinary field name (e.g. `step("x").result.value`).
- `internal/gen` round-trips the block and the reference, so host-generated
  threshold blocks re-emit exactly.
