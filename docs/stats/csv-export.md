# CSV export

`talon explain --csv` flattens every Decision the rules produced into a
single CSV stream on stdout, designed for downstream analysis in R, Python,
DuckDB, BigQuery, or any tool that reads CSV.

## Quick start

```sh
talon explain rules.tln tests.tln.test --csv > decisions.csv
```

In R:
```r
library(readr)
decisions <- read_csv("decisions.csv")
table(decisions$block, decisions$priority)
```

In Python:
```python
import pandas as pd
decisions = pd.read_csv("decisions.csv")
decisions.groupby(["block", "priority"]).size()
```

In DuckDB:
```sql
SELECT block, COUNT(*) FROM 'decisions.csv' GROUP BY 1 ORDER BY 2 DESC;
```

## Schema

One row per Decision, with these columns (stable order):

| Column | Type | Description |
|---|---|---|
| `test` | text | Test block name the Decision came from (`.tln.test`) |
| `block` | text | Rule block name that produced the Decision |
| `kind` | text | Block kind: `detect`, `forecast`, `rule`, etc. |
| `entity_id` | integer | The entity the Decision applies to |
| `entity_name` | text | Human-readable name if available; otherwise empty |
| `fired_at` | RFC 3339 timestamp | When the Decision was constructed (UTC) |
| `priority` | text | `LOW`, `MEDIUM`, `HIGH`, `CRITICAL`, or empty |
| `confidence` | text | `High`, `Medium`, `Low`, or empty |
| `action` | text | The rendered label/suggest template |
| `why` | text | Joined Why bullets, separated by ` | ` |
| `evidence` | text | Joined `attr=value` pairs, separated by `; ` |

## Why a flat schema (not a wide one)

A naive flat-export would explode every evidence key into its own column.
That breaks downstream tools whenever a rule adds or removes an evidence
key — analysts get column-mismatch errors on the next CSV load.

The current shape keeps the column set stable: when evidence keys change,
only the *content* of the `evidence` column shifts. Analysts who need the
full structured form should use `--json` instead and parse it with their
JSON library of choice.

If you genuinely need a wide schema for a specific rule (e.g., always
exactly `cost`, `urgency`, `pareto_rank` columns), the recommended
pattern is: parse the JSON, project the keys you want, write a wide CSV
yourself. The Talon `--csv` exporter intentionally stays general.

## Comparison with `--json`

| | `--csv` | `--json` |
|---|---|---|
| Column shape | Fixed, stable | Nested, follows Decision struct |
| Evidence | Flattened to single string | Full `[]Fact` array |
| TriggeredBy chain | Not rendered (use --json) | Nested under each Decision |
| Bytes per Decision | ~150-300 | ~500-1500 |
| Best for | Spreadsheets, SQL, data frames | Programs that need full structure |

## What's NOT in the CSV

`TriggeredBy` (cross-block chain, e.g., a `recommend` that points back to
its triggering `detect`) is **not** rendered in the CSV — it would either
explode rows (one per chain step) or require nested cells (which CSV
doesn't support). Use `--json` if you need the chain.

## Future: parquet

Parquet support was scoped out of this PR — it adds a dependency
(`xitongsys/parquet-go` ≈ 5MB) for what most users solve with CSV plus
`duckdb` or `pyarrow` on their side. If your CSV row count exceeds 10M
or you want column-typed reads natively, open an issue with the use case.
