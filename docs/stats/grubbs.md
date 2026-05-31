# Grubbs' single-outlier test

The default `is anomaly compared_to last N <unit>` clause uses z-score with
a hand-picked threshold (`z > 2.5`). Grubbs' test is the same shape with
a **statistical significance claim** instead: reject the null hypothesis
that the most extreme value comes from the same distribution as the rest,
at a configurable significance level α (default 0.05).

## When to use Grubbs over z-score

| Situation | Better choice | Why |
|---|---|---|
| You can't justify why threshold is 2.5 vs 3.0 vs 2.0 | **Grubbs** | α is interpretable: "we accept 5% false positives" |
| Small sample (n < 8) | **Grubbs** | z = 2.5 is unreachable for small n; Grubbs has the right critical value |
| Large sample (n > 50) | Either | Critical values converge; pick what your team is used to |
| Tenant pushed back on z=2.5 as "arbitrary" | **Grubbs** | Defensible as "rejected at 95% confidence" |
| Workload genuinely heavy-tailed (z-distribution assumption broken) | Neither; tune z via ABC | Both Grubbs and z assume normality |

## Syntax

Add `using grubbs` to the existing anomaly clause:

```talon
detect "Grubbs consumption outlier" {
  for records where type == "stock_item"
    and attr "weekly_consumption" is anomaly using grubbs compared_to last 12 weeks
  flag matching items
  label "{item.name}: {attr.weekly_consumption} — Grubbs-significant outlier"
  priority HIGH
}
```

Without `using grubbs` (or with `using zscore`), the existing z-score behavior
is unchanged.

## Algorithm

For each row with value x, compute the Grubbs statistic:

```
G = |x - mean| / sample_stddev
```

Note: **sample** standard deviation (Bessel-corrected, /n-1), not the
population stddev z-score uses. This matters at small n.

Reject the null (flag as outlier) when `G > G_crit(n, α)`, where the
critical value comes from the published Grubbs table at the chosen
significance level. v1 ships tabulated values for α ∈ {0.05, 0.01}
(NIST/SEMATECH Handbook §1.3.5.17); for other α the value is computed
from the closed form via a Cornish-Fisher Student's-t approximation
(accurate to ~1% for df ≥ 5).

## Evidence and explainability

Every Decision the Grubbs primitive produces carries:

- `G` — the computed statistic for this row
- `G_crit` — the threshold at the configured α
- `alpha` — the significance level used (0.05 by default)
- `mean`, `stddev`, `sample_n` — the sample statistics

`talon explain` surfaces these so an auditor can replay the call against
any stats package and verify Talon's claim:

```
EVIDENCE
  G          = 4.523
  G_crit     = 2.126
  alpha      = 0.05
  mean       = 49.75
  observed   = 250.0
  sample_n   = 8
  stddev     = 70.62
```

## Customizing α

Default is α = 0.05. Override per-call via Input.Params["alpha"] in tests,
or once an ABC tuning hook for Grubbs lands (analogous to z-score tuning,
deferred from this PR), via a labeled fixture.

## Sample-size limits

Minimum sample size: 3. Below that the Grubbs statistic is undefined.
The primitive returns `ErrSampleTooSmall` and falls through to the
testrunner's "keep all" behavior, matching how z-score handles short windows.

## What this doesn't do

- **Multiple outliers**: Classical Grubbs is for a *single* outlier. If
  multiple values are extreme, all may exceed G_crit and get flagged — but
  the type-I error claim only strictly holds for the single most extreme
  one. Use the generalized ESD (Rosner) for n>1 outliers; not implemented yet.
- **Non-normal data**: Grubbs assumes the bulk of the sample is normally
  distributed. For heavy-tailed or multimodal distributions, neither z-score
  nor Grubbs is the right tool — consider learned_threshold with a tuned
  percentile.
- **Per-entity time series**: Grubbs operates on a population at one point
  in time. Trend detection over multiple time points (Mann-Kendall) is a
  separate primitive that depends on time-series storage support that
  Talon's testrunner doesn't have yet.
