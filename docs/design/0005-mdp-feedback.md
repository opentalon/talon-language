# ADR-0005: MDP / bandit-style feedback for `recommend`

## Status

**Phase 1 shipped** (this PR) — single-arm Beta-posterior adaptive
ε-greedy with trace ID minting and feedback-fact ingestion. The
grammar is:

```tln
suggest "X" with probability 0.5 learn from feedback within 30 days
```

The executor queries `:feedback/block`/`outcome`/`at` facts within
the window, computes a Beta posterior with the declared
probability as prior, clamps to [0.01, 0.99], and gates suggestion
firing at the posterior rate. Each fired suggestion writes a
`:suggest/trace` fact so the host can correlate user actions back.

**Phase 2 deferred** — multi-arm bandits, contextual selection
(LinUCB / Thompson sampling), full MDP / Q-learning, off-policy
correction. Each needs more grammar surface (multiple `suggest`
per block, feature vectors, etc.).

## Context

`recommend` blocks today emit suggestions but nothing closes the
loop. If a user accepts or rejects a recommendation, that signal
goes nowhere. A real production system optimises future
recommendations against observed outcomes — bandit algorithms for
the simple case (contextual choice between K templates), MDPs when
the recommendation triggers state transitions whose long-term
reward depends on a policy.

The probabilistic `suggest "X" with probability N` shipped in this
PR is the first half: ε-greedy *exploration*. The missing half is
*exploitation* — adjusting N based on observed outcomes.

## Proposed shape

```tln
recommend "Stock replenishment" {
  when low_stock
  suggest "Order {qty} units"     with probability 0.8
  suggest "Hold and monitor"      with probability 0.2

  feedback {
    accept   when on assert "purchase_order" within 24 hours
    reject   when on retract "low_stock_alert" within 24 hours
    reward   accept = +1, reject = 0
  }

  optimize {
    method  thompson_sampling      ; or ucb1 / epsilon_greedy
    window  last 30 days           ; how far back to look for feedback
    update  daily                  ; how often to recompute probabilities
  }
}
```

The runtime would:
1. Stamp each fired suggestion with a unique trace ID into the FactStore.
2. The host (OpenTalon, tln-plugin) emits feedback facts as user
   actions happen — `:feedback/trace`, `:feedback/outcome`, `:feedback/at`.
3. A periodic tln job (cron, runtime hook, batch) reads recent
   feedback, computes posterior reward distributions per arm,
   updates the `with probability` parameters on the live block.

## Prerequisites (why this is its own PR)

1. **Telemetry plumbing** — recommendations need to leave a
   traceable mark so feedback can be joined back. Today's
   `BlockResult` doesn't carry per-row trace IDs.
2. **Persistent feedback storage** — feedback facts need durability
   across Runs. MemoryStore works for tests; production needs
   either Datalevin (current) or talon-db (#89) with a stable
   schema.
3. **Update cadence** — tln today is request-driven (`tln run`)
   or reactive (on-blocks). MDP needs scheduled re-evaluation. The
   host is the right place for cron (#17), so this PR also needs
   the host's scheduling story locked.
4. **Live parameter updates** — currently rule parameters are
   compile-time constants. MDP needs the probability to update at
   runtime without recompiling — likely a new namespace
   `:policy/<block>/<arm>` that the executor reads at recommend
   evaluation time.
5. **Calibration** — bandit / MDP algorithms have hyperparameters
   (exploration constant for UCB1, prior for Thompson sampling).
   tln needs a story for surfacing those — block annotation,
   global config, or auto-calibration via #76's tune machinery.

## Algorithm space (pick at implementation time)

| Algorithm | When it fits tln | Cost |
|---|---|---|
| **ε-greedy** | Simple, well-understood, easy to reason about. tln's `with probability N` is already shaped this way — just need to fold in observed rewards. | Lowest — small change to existing path |
| **UCB1** | Better regret bounds, no manual ε tuning. Needs accurate visit counts per arm. | Medium |
| **Thompson sampling** | Best empirical performance for Bernoulli rewards (accept/reject). Bayesian; surface as Beta priors per arm. | Medium |
| **Contextual bandits** (LinUCB / LinTS) | When the right arm depends on entity features (user segment, item category, etc.). | Higher — need feature vector access in recommend |
| **Full MDP / Q-learning** | When recommendations trigger state changes whose future reward matters (e.g. "approve" doesn't just earn now, it changes the trajectory). | Highest — state space + discount + policy iteration |

## Out of scope (deliberately)

- **Online learning during Run** — feedback signals arrive between
  Runs, not during them. No streaming gradient updates inside the
  executor.
- **Off-policy correction** — when the policy changes, historical
  data is sampled under a different distribution. Importance-
  sampling corrections are research-grade; v1 just uses a window
  of recent feedback and accepts the bias.
- **Multi-armed exploration with safety constraints** — "explore
  ten different stock-recommendation templates, but never suggest
  selling below cost." tln's existing `constraint` blocks should
  layer here; the MDP optimiser must filter candidate arms by
  hard constraints before sampling.

## Synergy with the other six features (this PR)

- **state_machine** — feedback events ARE state transitions on
  recommendation entities. `recommend` fired → state SUGGESTED;
  user accepted → state ACCEPTED. Reusing state machine machinery
  closes the loop architecturally.
- **event_sequence** — defining feedback as `accept when on assert
  "purchase_order"` is exactly an event-sequence match. The same
  regex-over-events engine answers "did user follow the suggested
  path?".
- **probabilistic suggest** — the *current* probability is the
  policy. MDP feedback turns it from a fixed constant into a
  learned value.
- **markov forecast** — long-run reward estimation under a policy
  is a Markov-chain calculation over the policy's induced state
  transitions. The runtime is already shipped.
- **HMM anomaly** — when feedback is noisy ("user clicked but
  didn't actually act on it"), HMM-style state inference picks
  the true outcome out of observable noise.

## Verification

This ADR ships when the implementation PR cites it. Concrete
acceptance criteria for the implementation PR:

- [ ] `recommend` blocks can declare a `feedback { ... }` clause
  and the parser/validator accept it.
- [ ] Suggestions write a trace ID to the FactStore on fire.
- [ ] A new plan step (or batch job) reads feedback facts within
  the declared window and recomputes per-arm probabilities.
- [ ] At least one algorithm (ε-greedy refinement) is fully wired
  end-to-end; UCB1 / Thompson can land as follow-ups behind a
  feature flag.
- [ ] The recommend executor reads the updated probability instead
  of the compile-time constant.

## What this ADR does NOT decide

- The specific learning algorithm (decided when the implementation
  PR opens; ε-greedy refinement is the safest first cut).
- The feedback storage schema (depends on Datalevin vs talon-db
  resolution — see #89).
- Whether the optimisation runs in-process or as a separate batch
  job. Probably batch.
- Where hyperparameter calibration lives (block annotation, host
  config, or `tune`). Depends on the chosen algorithm.
