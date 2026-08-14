# Stats + export

Two complementary additions to tln for working with statistical claims
and analyst tooling:

- **[Grubbs' test](./grubbs.md)** — `is anomaly using grubbs compared_to ...`
  replaces hand-picked z-thresholds with a defensible significance claim.
  Tabulated critical values at α ∈ {0.05, 0.01}; closed-form approximation
  for other α. Sample (Bessel-corrected) stddev, matching published tables.

- **[CSV export](./csv-export.md)** — `tln explain --csv` writes Decisions
  to a stable column schema for R, Python, DuckDB, etc. No new dependencies.
  Designed to compose with the existing `--json` mode rather than replace it.

## Reading order

1. Try the example: `tln test examples/grubbs_consumption.tln test/grubbs_consumption.tln.test`
2. Read [grubbs.md](./grubbs.md) for the algorithm + when to prefer Grubbs over z-score
3. Pipe the explain output to CSV: `tln explain ... --csv > out.csv`, then read in R / Python / SQL
4. Read [csv-export.md](./csv-export.md) for the schema + comparison with `--json`

## What's not here

- **Mann-Kendall trend test** — non-parametric monotonic trend detection.
  Needs per-entity time-series storage that tln's testrunner doesn't
  support yet. Deferred until that infrastructure exists.
- **Welch's t-test** — two-sample mean comparison. Niche enough that no
  tenant has asked for it; will land when there's a concrete use case.
- **Parquet export** — CSV solves ~95% of the analyst-export need.
  Parquet would add a ~5MB dependency; defer until a user hits CSV's limits.
- **R client SDK** — would let R users embed tln rules in their pipelines.
  Out of scope; the export-only path (Option A from the design discussion)
  is what we chose to keep deployment dead simple.
