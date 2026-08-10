# The Talon REPL

`talon repl` is an interactive read-eval-print loop. Assert facts, define
blocks inline, and evaluate them — including step-by-step trace output so
you can see *why* a decision was made, not just *what* the decision was.

## Why a REPL

Without one, the loop is: write file → run compiler → read error → edit →
rerun. With a REPL: type a fact, type a rule, see it fire. The fast loop
matters most when you're learning the language, debugging a misbehaving
rule, or prototyping policy against fresh data.

## A worked example: insurance-claims auto-adjudication

This walkthrough uses [`examples/insurance_claims.tln`](../examples/insurance_claims.tln)
and [`test/insurance_claims.tln.test`](../test/insurance_claims.tln.test).
It's modelled on the worked example in
[Code Mode for MCP](https://opakalex.github.io/posts/code-mode-for-mcp/) —
an LLM extracts facts from claim invoices, Talon makes the deterministic
auto-approve / reject / escalate decision.

Load the rules and the test fixture's facts:

```
$ talon repl
talon dev — type :help for commands, :quit to exit
talon> :load examples/insurance_claims.tln
  loaded examples/insurance_claims.tln: 5 block(s), 0 fact(s)
talon> :load test/insurance_claims.tln.test
  loaded test/insurance_claims.tln.test: 0 block(s), 28 fact(s)
```

Note `:load` of a `.tln.test` file pulls the `given { … }` facts into
the session — useful for replaying a known scenario interactively.

See what's loaded:

```
talon> :rules
  5 block(s):
    rule          "Auto-approve in-network routine"
    detect        "Out-of-network provider"
    detect        "Over the per-visit cap"
    rule          "Reject blacklisted provider"
    recommend     "Schedule reviewer"
```

Evaluate every block at once:

```
talon> :eval all
  "Auto-approve in-network routine": 1 detection(s) — records [902]
  "Out-of-network provider":         1 detection(s) — records [904]
  "Over the per-visit cap":          2 detection(s) — records [903 904]
  "Reject blacklisted provider":     1 detection(s) — records [901]
  "Schedule reviewer":               0 detections
```

Read this as: the four claims in the fixture each match the block that
applies to them. Claim 904 matches *both* "out-of-network" and "over the
cap" — perfectly reasonable; it's both, and both detect blocks flag it
independently.

## Debug output: `:trace`

`:eval` tells you the answer. `:trace` tells you how Talon got there.

```
talon> :trace "Auto-approve in-network routine"
  "Auto-approve in-network routine": 1 detection(s) — records [902]
    trace:
    step 1  DatalevinQuery → candidates  (rows: [902])
```

Reading the trace line by line:

| Field | Meaning |
| --- | --- |
| `step 1` | Position in the query plan |
| `DatalevinQuery` | The kind of step — Datalog query against the fact graph |
| `→ candidates` | The variable name the step writes into |
| `(rows: [902])` | Which record IDs the step matched |

For a rule with multiple conditions, you see multi-step plans:

```
talon> :trace "Over the per-visit cap"
  "Over the per-visit cap": 2 detection(s) — records [903 904]
    trace:
    step 1  DatalevinQuery → candidates  (rows: [903 904])
    step 2  GoComputation render_template → detections
```

Step types you'll see today:

| Step type | What it does |
| --- | --- |
| `DatalevinQuery` | Selects facts from the fact graph by EAV pattern + predicates |
| `GoComputation` | Renders labels, resolves block-matches, runs optimizers (Pareto/GA/ACO/ILP) |
| `MLComputation` | Runs an ML primitive: anomaly detection, forecast, predict, cluster, similar, PPR |
| `Filter` | Narrows a previous step's output by an additional condition |

If a trace shows zero rows on step 1, you know the selector didn't match
any facts — either the data isn't there or the condition is too strict.
If it matches on step 1 but drops to zero by step 3, the ML primitive
or filter is responsible. The step number is the place to look.

## Finding and counting facts

`:find` and `:count` apply a selector to the session's facts. Both accept
the full `for records where …` form, or a shorthand condition — the REPL
fills in the boilerplate:

```
talon> :find type == "claim"
  901
  902
  903
  904

talon> :count attr "provider_status" == "in_network"
  2

talon> :count attr "amount_chf" > 1000
  1
```

A note on syntax: bare identifiers like `type`, `status`, and `category`
are first-class fact keys. Any other attribute needs the `attr "name"`
form — that's a property of the language, not the REPL.

## Assert facts inline

You don't need a fixture file. Type facts directly:

```
talon> record 999 type "claim"
  OK: record 999
talon> attr 999 "provider_status" "in_network"
  OK: attr 999
talon> attr 999 "service_category" "outpatient"
  OK: attr 999
talon> attr 999 "amount_chf" 320
  OK: attr 999
talon> attr 999 "per_visit_cap" 500
  OK: attr 999
talon> :eval "Auto-approve in-network routine"
  "Auto-approve in-network routine": 1 detection(s) — records [902, 999]
```

`record N <key> <value> [<key> <value> ...]` and `attr N "key" <value>`
are REPL-only shortcuts. Internally they synthesise the same data the
`given { … }` block produces in a test fixture.

## Define blocks inline

Paste or type a block; the REPL recognises unbalanced braces and switches
to a `..` continuation prompt until the body closes:

```
talon> detect "High-value claim" {
  ..   for records where type == "claim"
  ..     and attr "amount_chf" > 1000
  ..   flag matching items
  ..   label "{item.id}: {attr.amount_chf} CHF — over 1000"
  .. }
  OK: detect "High-value claim"
talon> :eval "High-value claim"
  "High-value claim": 1 detection(s) — records [903]
```

Redefining a block by name replaces the previous version — useful when
you're iterating on a rule.

## The iteration loop

The point of a REPL is the tight loop between *try → see → adjust*. Here's a
full session from empty to a working narrowed rule, with the output you'll
actually see:

```
$ talon repl
talon> detect "Active items" {
  ..   for records where type == "item" and status == "active"
  ..   flag matching items
  .. }
  OK: detect "Active items"

talon> :eval "Active items"
  "Active items": 0 detections
```

Empty store, so zero detections. Add some facts and re-eval:

```
talon> record 501 type "item" status "active"
  OK: record 501
talon> attr 501 "name" "VW Transporter"
  OK: attr 501
talon> record 502 type "item" status "active"
  OK: record 502
talon> attr 502 "name" "Ford Transit"
  OK: attr 502
talon> :eval "Active items"
  "Active items": 2 detection(s) — records [501 502]
```

Both records match. Now tighten the rule by redefining it inline — same
name, so the new definition replaces the old:

```
talon> detect "Active items" {
  ..   for records where type == "item"
  ..     and status == "active"
  ..     and attr "name" == "VW Transporter"
  ..   flag matching items
  .. }
  OK: detect "Active items"

talon> :eval "Active items"
  "Active items": 1 detection(s) — records [501]
```

One fewer match — the Ford was filtered out. If you want to start over on
the data without losing the block:

```
talon> :clear facts
  cleared facts; blocks kept

talon> :facts
  no facts in memory

talon> :eval "Active items"
  "Active items": 0 detections
```

Block definitions are still loaded, so adding fresh facts immediately
exercises them again. `:clear` (without arguments) drops everything —
facts, blocks, and context — for a clean slate.

A practical iteration pattern when debugging a rule that doesn't fire:

1. `:trace "rule name"` — see which step drops the row set to zero.
2. `:find …` — confirm the facts you think are there really are there.
3. Redefine the block with a relaxed condition; `:eval` again.
4. Once it fires, tighten back step by step.

This is faster than the file-edit-recompile loop, and trace output
shows exactly which clause is doing the filtering — no guesswork.

## Other commands

| Command | What it does |
| --- | --- |
| `:facts` | List facts currently in the session, grouped by record |
| `:rules` | List compiled blocks |
| `:clear` | Drop facts, blocks, and context |
| `:clear facts` | Drop facts only — keep your block definitions |
| `:context KEY VALUE` | Set a context variable (e.g. `:context role "manager"`) |
| `:help` | Show the full command list |
| `:quit` | Exit (Ctrl-D / EOF also works) |

## What's *not* in this pass

- `:connect`, `:mcp` — connected-mode MCP tool calls (needs the MCP layer
  threaded through the session; tracked under [#17](https://github.com/opentalon/talon-language/issues/17)).
- `:check rule "name" tool "x"` — policy-rule probe against a synthetic
  tool call. Needs the same MCP plumbing.
- Readline history / arrow keys / tab completion — first-pass uses
  `bufio.Scanner` so the binary stays static and the dependency tree
  stays small. A future build flag may add `peterh/liner` or
  `chzyer/readline`.
