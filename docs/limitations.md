# Datalevin limitations — automata & Markov feature set

This document tracks what *doesn't* work cleanly when the
state_machine, event_sequence, probabilistic suggest, Markov
forecast, HMM anomaly, and substate features land against the
shipped Datalevin backend (v0.10.7).

The TL;DR: every feature in the automata-markov PR ships fully
working against `MemoryStore`. Against Datalevin, some features
have real gaps because Datalog (and Datalevin's particular
implementation choices) don't natively express what the runtime
needs. We call those out explicitly so:

1. Production deployments know what to expect.
2. The talon-db Phase-3a (#89) design has explicit requirements.
3. Users running against the JVM sidecar don't quietly hit edge
   cases that work in REPL tests.

## state_machine

| Concern | Status against Datalevin |
|---|---|
| Reading current state via `:record/state` Pattern | ✅ Works — standard EAV query |
| Writing new state via `Assert(:record/state = newValue)` | ✅ Works — single tx-data row |
| Cross-block visibility of new state | ✅ Same Run sees the assertion; subsequent blocks read it |
| Atomicity across multi-entity transitions | ⚠️ Each entity's transition is a separate `/transact` call — no batch atomicity. Failure mid-batch leaves the FactStore in a partially-updated state. **Fix in talon-db**: batch all transition writes into one tx. |
| History of state changes | ❌ Datalevin doesn't expose `:as-of` (see PR #87 discussion). We can't reconstruct "what state was this entity in at tx 12345" from Datalevin alone. Workaround: emit explicit `:event/state-change` facts alongside the state attribute. |
| Concurrent state machines on the same entity | ⚠️ Two blocks declaring `:record/state` race-write — last writer wins. The runtime doesn't currently detect this overlap. **Fix**: validator pass that rejects overlapping state attributes. |
| Hierarchical substates (`parent/child`) | ✅ Works — state value is a plain string; the executor's `stateMatches` handles the prefix semantics in-memory. |

## event_sequence

| Concern | Status against Datalevin |
|---|---|
| Querying events by entity ID | ✅ Works — `[?ev :event/entity 501]` pattern |
| Querying events sorted by time | ⚠️ Datalevin returns rows in index order, not sorted by `:event/at`. The executor sorts in-memory per entity. Fine for small histories (< 10k events per entity); falls over at scale. **Fix in talon-db**: per-entity sorted index on `:event/at`. |
| Sliding-window expiry of old events | ❌ Datalevin keeps everything; the executor walks the entire history and discards out-of-window matches. **Fix**: TTL on events via `:event/expires` + a sweep job, *or* a windowed materialised view in talon-db. |
| Concurrent event ingestion + sequence detection | ⚠️ The executor reads events at query time; if events arrive between candidate-row fetch and sequence-check, they're missed in this Run. Reactive on-blocks would catch them; CLI Run won't. |
| Regex-over-events richer than ordered sequence | ❌ Not implemented. Current support: `A -> B -> C within N days`. Kleene star (`A -> B+ -> C`), branches (`A -> (B \| C) -> D`), and timing constraints between specific steps (`A -> B within 1h, B -> C within 24h`) all need follow-up work. |

## Probabilistic `suggest`

| Concern | Status against Datalevin |
|---|---|
| Deterministic per-Run sampling | ✅ Seeded RNG; same RandSeed + same block name = same outcome |
| Cross-Run reproducibility | ✅ Block-name FNV seeding stays stable |
| Outcome telemetry (which suggestion was accepted?) | ❌ No telemetry today — see ADR-0005 (MDP feedback). Datalevin would have to store trace IDs per fired suggestion. |
| Live probability updates between Runs | ❌ Probability is compile-time constant. Bandit / MDP updates need a separate facts namespace `:policy/<block>/<arm>` and runtime read. |

## Markov forecast (`mlruntime.MarkovChain`)

| Concern | Status against Datalevin |
|---|---|
| Building a chain from observed sequence | ✅ Pure in-memory algorithm; consumes a `[]string` |
| Reading state history from Datalevin | ⚠️ Today the runtime accepts a `[]string` directly; the language-level wiring (forecast block reading `:event/state` facts) ships in a follow-up. Same `:event/at` sorting issue applies. |
| Time-step granularity | ⚠️ The chain is "1 transition" — no notion of "transitions per day". Predicting "P(state = SHIPPED *within 7 days*)" needs a separate calendar-aware model. v1 answers "P(state = SHIPPED after N transitions)" only. |
| Higher-order chains | ❌ First-order only. P(next \| last 2 states) needs a different state-space representation. |
| Continuous-time Markov chains | ❌ Discrete-step only. Real-time decay (exponential interarrival) requires CTMC math, separate work. |

## HMM anomaly (`mlruntime.HMM`)

| Concern | Status against Datalevin |
|---|---|
| Forward algorithm scoring | ✅ Pure algorithm; log-domain stable |
| Per-entity observation sequence | ⚠️ Same sorting + windowing concerns as event_sequence |
| Model parameters | ✅ Inline (initial, trans, emit matrices) — host owns model storage |
| Training (Baum-Welch) | ❌ Not implemented. Models must be trained outside tln (Python scikit-learn, Stan, etc.) and parameters supplied as constants. |
| Continuous observations | ❌ Discrete symbols only. Gaussian-emission HMMs need a different runtime. |
| Online state inference (Viterbi) | ❌ Only forward-algorithm scoring (P(obs \| model)). Viterbi (most likely hidden state sequence) would be ~100 LOC more if needed. |

## Substates

| Concern | Status against Datalevin |
|---|---|
| Parent-state transitions matching children | ✅ String-prefix match in executor |
| Initial substate when entering parent | ⚠️ Declared in `ast.StateDecl.Initial` but executor doesn't yet honour it — transitions targeting a parent stay at the parent's literal name. **Fix**: small executor change that rewrites the target to `parent/initial` when targeting a composite parent. |
| Deep nesting (`a/b/c`) | ❌ Parser accepts one level (`parent/child`). Three levels need parser changes + recursive prefix matching. |
| History pseudo-states (Harel-style "return to last substate") | ❌ Not implemented. Needs a separate `:record/last-substate` attribute per parent. |
| Parallel regions | ❌ Not in scope — current AST is single-state-per-entity. Parallel regions need a fundamentally different model. |

## MDP feedback (bandit-style adaptive ε-greedy)

| Concern | Status against Datalevin |
|---|---|
| Beta-posterior update from accept/reject feedback | ✅ Works — executor queries `:feedback/block`/`outcome`/`at` facts and computes posterior live |
| Trace ID per fired suggestion | ✅ Works — `:suggest/trace`/`block`/`entity`/`at` facts written on fire |
| Compile-time prior probability | ✅ Works — `suggest "X" with probability 0.5 learn from feedback within 30 days` |
| Posterior clamping (avoid ε=0 / ε=1 lock-in) | ✅ Works — clamps to [0.01, 0.99] |
| Multi-arm bandits (multiple `suggest` per block) | ❌ Single-suggest only — grammar doesn't yet support multiple competing arms per recommend |
| UCB1 / Thompson sampling | ❌ Beta-mean only. Algorithm choice deferred until multi-arm grammar exists |
| Off-policy correction | ❌ Posterior assumes feedback was drawn under the current policy; if probability changes during the window, the estimate is biased. Importance-sampling correction is research-grade |
| Cross-tenant policy isolation | ⚠️ Feedback facts inherit tenant scope (per PR #87 routing) — works, but the host must wire each tenant's feedback ingest separately |
| Window-based feedback ordering | ⚠️ Same `:event/at` in-memory sort applies — fine at scale of thousands of feedback events per block, falls over at millions |
| Live policy updates between Runs | ✅ Recompute on every recommend evaluation; no caching today (acceptable cost for low-frequency recommend blocks) |

## What lands cleanly in MemoryStore but not Datalevin (yet)

These are the integration gaps an embedded deployment notices when
swapping from MemoryStore to Datalevin:

1. **Multi-entity transition atomicity** — state_machine writes per
   entity, no batch.
2. **Event sorting** — runtime sorts in-memory; doesn't scale.
3. **Sliding-window event expiry** — no TTL.
4. **Time-travel (`:as-of`)** — Datalevin 0.10.7 simply doesn't
   have the API. Blocks recommend / detect over historical state.
5. **Multi-tenant isolation for event facts** — works (PR #87
   routes per-tenant DB), but cross-tenant queries (rare but real
   for ops dashboards) need separate consideration.
6. **Probability parameter live updates** — needs a place for
   policy facts to live; reactive load on a Run is fine but
   needs writing through Datalevin's tx model.

## What's blocking each from talon-db (#89)

When talon-db ships, the following gaps close cleanly because we
own the storage layer:

- **`:as-of` time-travel** — talon-db indexes by tx-time
- **Per-entity sorted event indexes** — straightforward LSM design
- **Batch transitions** — exposed as a multi-write Assert
- **Beta-memory persistence for RETE** — needed for incremental
  reactive on-blocks consuming derived facts (#91, ADR-0004)
- **Policy fact namespace** — first-class, queryable by recommend
  executor at runtime

The talon-db milestone is the natural home for closing these gaps;
this PR ships the language surface and proves the algorithms
locally so the storage layer's requirements are concrete.
