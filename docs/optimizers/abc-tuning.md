# Artificial Bee Colony — ML primitive auto-tuning

The first four optimizer backends (Pareto, GA, ACO, ILP) live in `combine`
blocks and answer "which records?". **ABC tuning** lives one layer down,
in `detect`/`predict`/`forecast` blocks, and answers a different question:
"what *threshold* should the ML primitive use against *this tenant's* data?"

## Why this exists

The seven ML primitives in `internal/mlruntime` (`anomaly_zscore`,
`learned_threshold`, etc.) ship with hardcoded defaults. `anomaly_zscore`'s
z = 2.5 is roughly the p99 of a standard normal — a perfectly reasonable
starting point for some data, badly miscalibrated for others.

Inventory consumption is rarely normal. Heavy tails, weekday cycles, supplier
seasonality, batch reorders — all of these shift where "anomalous" actually
starts. A threshold tuned for one warehouse will flood another with false
positives.

ABC tuning lets each tenant's rule learn its own threshold from a labeled
sample of past incidents, while keeping the rule itself — and its audit
trail — completely deterministic.

## Syntax

```talon
detect "Tuned consumption anomaly" {
  for records where type == "stock_item"
    and attr "weekly_consumption" is anomaly compared_to last 12 weeks
  tune against test "labeled_consumption_history"
  flag matching items
  label "{item.name}: anomalous"
  priority HIGH
}
```

The `tune against test "name"` clause names a labeled `test` block (in the
same `.tln.test` file passed to `talon test` or `talon explain`):

```talon
test "labeled_consumption_history" {
  given {
    // ... 12 stock_items with weekly_consumption values ...
  }
  when detect "Tuned consumption anomaly"
  expect {
    flagged 411    // tenant's incident log says this was real
    flagged 412
    not flagged 401   // tenant verified normal
    not flagged 402
  }
}
```

Talon reads the `expect flagged X` lines as **ground-truth positive labels**;
entities in `given` that aren't listed positive are negative under
closed-world assumption.

## What happens at evaluation time

At `talon test` / `talon explain`:

1. The testrunner scans every detect block for a `tune` clause.
2. For each, it locates the named labeled test, extracts entities + labels.
3. **ABC runs**: searches z-threshold ∈ [0.5, 4.0] with 16 bees × 40
   iterations (≈ 1,280 fitness evaluations). Fitness = F1 against the labels.
4. The optimal threshold is cached per detect block and injected into the
   anomaly primitive's `Params["threshold"]` at every subsequent evaluation.

Cost: 40-100ms of one-time ABC for a 100-row fixture. Subsequent evaluations
use the cached value with no overhead.

## What `talon explain` shows

```
COMBINE   Tuned consumption anomaly — Coolant (entity #411) flagged
WHY
  • threshold auto-tuned via ABC against test "labeled_consumption_history" — z=1.81, F1=1.00 (P=1.00, R=1.00) on 12 samples

EVIDENCE
  tuned_against    = labeled_consumption_history
  tuned_f1         = 1.0
  tuned_precision  = 1.0
  tuned_recall     = 1.0
  tuned_threshold  = 1.81
  weekly_consumption = 70
```

Auditors get the whole provenance chain: which fixture taught the threshold,
what F1 it achieved, what precision/recall trade-off was actually selected,
how big the sample was. Removing or relabeling the fixture changes the
threshold; the audit trail tells you when and why.

## Worked example: heavy-tailed consumption

`examples/tuned_consumption.tln` + `test/tuned_consumption.tln.test`
ship as the canonical demonstration. The labeled fixture has:

- 10 routine consumption values around mean 50 (stddev ≈ 1)
- 2 outliers at 70 and 72 (z ≈ 2.1 and 2.4 respectively)

Under default z = 2.5, **neither outlier flags** — both fall below the
threshold even though the tenant flagged them as real incidents.

After ABC tuning: z = 1.81, F1 = 1.00. Both outliers flag; nothing else does.

## Algorithm

Karaboga's Artificial Bee Colony (2005):

1. **Initialize**: 8 food sources (= ColonySize / 2), each a random
   threshold in [0.5, 4.0].
2. **Per iteration (40 total)**:
   - **Employed phase**: each bee perturbs its food source by v = x + φ(x - x_k)
     where x_k is a random other source, φ ∈ [-1, 1]. Replace if fitness improves.
   - **Onlooker phase**: each onlooker bee picks a food source weighted by
     fitness, perturbs it the same way. Replace if better.
   - **Scout phase**: any source with `limit` consecutive failed improvements
     is abandoned — a scout restarts it from a random position.
3. Return the best threshold ever seen.

ABC's signature feature is **scout-driven restart** — when a region of the
search space goes stale, the algorithm automatically escapes by sampling
fresh territory. For threshold tuning that means: no risk of locking in
on a bad local optimum just because the initial population happened to
cluster there.

Two control parameters (colony size, abandonment limit) vs GA's four. Less
to tune; more robust defaults.

## What's tunable (today)

| ML primitive | Parameter | Range | Status |
|---|---|---|---|
| `anomaly_zscore` | `threshold` (z value) | [0.5, 4.0] continuous | ✅ Shipped |
| `learned_threshold` | `method` (`p<N>` percentile) | [50, 99] → `p<int>` | ✅ Shipped |
| `forecast_exponential_smoothing` | α smoothing | [0, 1] | Awaits primitive |
| `predict_decision_tree` | confidence cutoff | [0, 1] | Awaits primitive |
| `cluster_dbscan` | ε, minPts | continuous + discrete | Awaits primitive |
| `classify_knn` | k | discrete | Awaits primitive |
| `similarity_cosine` | threshold | [0, 1] | Awaits primitive |

Five of the seven ML primitives are language surface today but not yet
implemented as real Go code — they parse and plan but fall through to a
stub at runtime. Tuning a stub is meaningless, so those entries wait
until the underlying primitives ship. The tuning registry
(`internal/testrunner/tuning.go`'s `tunables` slice) is ready: adding a
new line per primitive enables ABC against it the day the primitive lands.

## Worked example: learned_threshold percentile

`examples/tuned_high_mileage.tln` demonstrates `learned_threshold`
tuning. The rule uses `attr "km" > learned_threshold p95 of attr "km"`,
but the labeled fixture proves p95 is too lax for *this* fleet:

- 10 routine vehicles at 30,000–45,000 km
- 2 tenant-flagged early-service candidates at 60,000 and 65,000 km

With default p95, only the very top outlier flags (65,000); 60,000 is
just below the cutoff. ABC searches percentile ∈ [50, 99], rounds to
integer, and finds **p90 with F1 = 1.00** — both labeled vehicles now
flag, nothing else does.

`talon explain` renders:

```
WHY
  • method auto-tuned via ABC against test "labeled_high_mileage"
    — method=p90, F1=1.00 (P=1.00, R=1.00) on 13 samples

EVIDENCE
  tuned_method     = p90
  tuned_against    = labeled_high_mileage
  tuned_f1         = 1.0
  tuned_precision  = 1.0
  tuned_recall     = 1.0
```

The discrete-percentile encoding is handled by the tunable's `Encode`
hook: ABC searches the continuous range and the encoder rounds to the
nearest integer + formats as the `p<int>` string the primitive expects.
Generic enough to handle other integer-rounded tunables (k in k-NN,
minPts in DBSCAN) without algorithm changes.

## When NOT to use tuning

- **You don't have labeled history.** ABC needs ground-truth positives and
  negatives — without them, "tuning" is meaningless and the result is the
  starting point. Use the default threshold.
- **Your labeled fixture is tiny.** With < 10 labeled positives the tuned
  threshold is statistical noise. The testrunner doesn't block this case —
  small samples just produce unstable thresholds across runs. Add a bigger
  fixture or stick with defaults.
- **Auditability matters more than accuracy.** Per-tenant thresholds change
  with the data; "this rule fired because z > 2.5" is one less moving part
  than "this rule fired because z > 1.81, which was learned from fixture
  X on date Y." Pick what your auditors will accept.

## Reproducibility

ABC is stochastic. v1 uses a fixed seed (42) so the same fixture always
produces the same threshold. A future revision will accept a per-block
`seed N` clause for explicit control. If you regenerate the labeled
fixture, the threshold may change — that's the design.

## Comparison with GA tuning

Could you use GA instead of ABC for the same problem? Yes. ABC's
advantages here:

- **Fewer parameters**: no crossover rate, no mutation rate.
- **Scout phase**: automatic escape from local optima.
- **Built for continuous search**: GA's discrete mutation is awkward on
  a continuous threshold.

GA wins for *combinatorial* problems with structured solutions
(subsets, permutations). For continuous scalar tuning, ABC is the
better fit. That's why we placed it here, not as a `combine` backend.

## When to add more dimensions

Right now ABC tunes a single scalar (the z-threshold). If we extend to,
say, DBSCAN — which needs ε *and* minPts together — ABC handles that
natively (2-D bounded search). The Talon language surface stays the
same (`tune against test`); the internal change is which primitive's
parameter space we register.

The pattern: any ML primitive with a continuous (or quasi-continuous)
parameter space and a labeled fitness signal can become tunable by:

1. Adding bounds (`internal/optimize/tunables.go` — to be added when the
   second tunable lands).
2. Wiring the parameter into the primitive's `Params`.
3. Extending `findTunableStep` to recognize the function.

Future docs revision will document this contract once the second
tunable primitive ships.
