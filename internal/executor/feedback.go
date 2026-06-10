package executor

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/opentalon/talon-language/internal/factstore"
)

// adjustWithFeedback turns a recommend's compile-time
// `probability` into a Beta-posterior estimate of the true
// accept-rate observed over the last `windowDays`. The compile-
// time value sets the Beta prior's mean (α / (α + β)) with a
// pseudo-count of 1 so a few observations move the estimate
// meaningfully — but the prior dominates when feedback is sparse
// (the right behaviour for a freshly-deployed block).
//
// Feedback facts in the FactStore are EAV triples on dedicated
// feedback entities:
//
//	:feedback/block     <block name>           (str)
//	:feedback/outcome   "accept" | "reject"    (str)
//	:feedback/at        <unix seconds>         (long)
//
// The host (OpenTalon plugin, CLI ingester) emits these as user
// actions arrive. Reading them is one Query per recommend
// invocation; trivially indexed by `:feedback/block` on talon-db.
func (e *Executor) adjustWithFeedback(ctx context.Context, blockName string, prior float64, windowDays int) (float64, error) {
	if windowDays <= 0 {
		return prior, nil
	}
	cutoff := float64(time.Now().Add(-time.Duration(windowDays) * 24 * time.Hour).Unix())

	q := factstore.Query{
		Find: []string{"?fb", "?outcome", "?at"},
		Where: []factstore.Clause{
			&factstore.Pattern{Entity: factstore.Var("fb"), Attribute: ":feedback/block", Value: factstore.Lit(blockName)},
			&factstore.Pattern{Entity: factstore.Var("fb"), Attribute: ":feedback/outcome", Value: factstore.Var("outcome")},
			&factstore.Pattern{Entity: factstore.Var("fb"), Attribute: ":feedback/at", Value: factstore.Var("at")},
		},
	}
	rows, err := e.Client.Query(ctx, q)
	if err != nil {
		return prior, err
	}

	accepts, rejects := 0, 0
	for _, r := range rows {
		if len(r) < 3 {
			continue
		}
		at, ok := r[2].(float64)
		if !ok {
			if i, ok2 := r[2].(int64); ok2 {
				at = float64(i)
			} else if i, ok2 := r[2].(int); ok2 {
				at = float64(i)
			} else {
				continue
			}
		}
		if at < cutoff {
			continue
		}
		outcome, _ := r[1].(string)
		switch outcome {
		case "accept":
			accepts++
		case "reject":
			rejects++
		}
	}

	// Beta posterior. Prior mean = prior; map to (α, β) with a
	// total pseudo-count of 1: α₀ = prior, β₀ = 1 - prior. After
	// observing `accepts` and `rejects`, posterior mean is
	//   (α₀ + accepts) / (α₀ + β₀ + accepts + rejects)
	//   = (prior + accepts) / (1 + accepts + rejects)
	// With zero feedback this returns prior unchanged.
	posterior := (prior + float64(accepts)) / (1 + float64(accepts+rejects))
	// Clamp into (0.01, 0.99) so a string of unanimous outcomes
	// doesn't lock the policy at 0 or 1 — keeps a sliver of
	// exploration alive (classical ε-floor / ε-ceiling).
	if posterior < 0.01 {
		posterior = 0.01
	}
	if posterior > 0.99 {
		posterior = 0.99
	}
	return posterior, nil
}

// mintTraces writes one `:suggest/trace` fact per fired row so the
// host can correlate later user-action telemetry back to the
// specific suggestion that prompted it. Trace IDs are stable
// across the executor's lifetime so a single Run produces stable
// traces; concurrent Runs across processes need a globally-unique
// scheme (UUIDs) — out of scope here, the host generates them.
//
// Returns the slice of generated trace IDs in row order so callers
// (template rendering, downstream consumers) can include them in
// the output. Best-effort: an Assert failure logs but doesn't
// abort the recommend.
func (e *Executor) mintTraces(ctx context.Context, blockName string, rows [][]any) ([]string, error) {
	now := time.Now().Unix()
	ids := make([]string, 0, len(rows))
	facts := make([]factstore.Fact, 0, len(rows)*3)

	for i, row := range rows {
		if len(row) == 0 {
			continue
		}
		entityID, _ := toIntSM(row[0])
		// trace_id: <block-name>-<now>-<row-index>. Stable within a
		// Run, distinct across rows. Hosts that need cross-Run
		// uniqueness should wrap the executor's mint with a UUID
		// shim — keeping the executor portable for tests.
		traceID := fmt.Sprintf("%s-%d-%d", blockName, now, i)
		// Synthesise a record ID for the feedback entity. We use
		// negative IDs to avoid colliding with user-facing
		// records; talon-db will eventually allocate proper
		// internal IDs but FactStore doesn't expose that yet.
		recordID := strconv.FormatInt(-now-int64(i)-1, 10)
		facts = append(facts,
			factstore.Fact{RecordID: recordID, Attribute: ":suggest/trace", Value: traceID},
			factstore.Fact{RecordID: recordID, Attribute: ":suggest/block", Value: blockName},
			factstore.Fact{RecordID: recordID, Attribute: ":suggest/entity", Value: int64(entityID)},
			factstore.Fact{RecordID: recordID, Attribute: ":suggest/at", Value: float64(now)},
		)
		ids = append(ids, traceID)
	}

	if err := e.Client.Assert(ctx, facts); err != nil {
		// Don't fail the recommend on telemetry-write failures —
		// the suggestion already fired; lost trace IDs are a
		// data-quality issue, not a correctness one.
		return ids, fmt.Errorf("mint traces (recommend %q): %w", blockName, err)
	}
	return ids, nil
}
