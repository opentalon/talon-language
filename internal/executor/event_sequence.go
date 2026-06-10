package executor

import (
	"context"
	"sort"
	"time"

	"github.com/opentalon/talon-language/internal/factstore"
	"github.com/opentalon/talon-language/internal/planner"
)

// execEventSequence filters Input rows to entities whose event
// history contains the requested ordered sequence within the
// declared time window. Event facts are stored as their own
// entities — schema:
//
//	:event/entity   the subject the event pertains to
//	:event/name     short string label (e.g. "cart_opened")
//	:event/at       timestamp in seconds since epoch
//
// The runtime walks events for each candidate sorted by :event/at
// and checks for an ordered subsequence matching Steps such that
// (last.at - first.at) ≤ WindowSeconds. This is the matcher of a
// star-free regex `S0 .* S1 .* S2 ...` — the user's "regex over
// event streams".
//
// Window of 0 is treated as "unbounded" so callers can express
// "ever, in any order" with the existing surface (write Steps in
// the only order that should match, leave Window out — parser
// gives Duration{0, ""}, which durationToSeconds maps to 0).
func (e *Executor) execEventSequence(ctx context.Context, s *planner.EventSequenceStep, vars map[string]any) (StepResult, error) {
	rows, _ := vars[s.Input].([][]any)
	if len(s.Steps) == 0 || len(rows) == 0 {
		vars[s.Into] = rows
		return StepResult{Type: "EventSequenceStep", Name: s.BlockName, Output: rows}, nil
	}

	kept := make([][]any, 0, len(rows))
	for _, row := range rows {
		eid, ok := toIntSM(row[0])
		if !ok {
			continue
		}
		events, err := e.eventsFor(ctx, eid)
		if err != nil {
			return StepResult{}, err
		}
		if matchesSequence(events, s.Steps, s.WindowSeconds) {
			kept = append(kept, row)
		}
	}
	vars[s.Into] = kept
	return StepResult{
		Type:   "EventSequenceStep",
		Name:   s.BlockName,
		Output: kept,
	}, nil
}

// eventRecord is one event for a target entity, used internally by
// the matcher.
type eventRecord struct {
	name string
	at   float64 // seconds since epoch (or any monotonic time the host uses)
}

// eventsFor returns every event fact recorded for entity `eid`,
// sorted by :event/at. Each event is its own EAV entity with the
// :event/entity attribute pointing at the target — i.e. event
// facts are first-class, queryable independently. That's the
// pattern: events are immutable additions to the store, not
// mutations of the target entity.
func (e *Executor) eventsFor(ctx context.Context, eid int) ([]eventRecord, error) {
	q := factstore.Query{
		Find: []string{"?ev", "?name", "?at"},
		Where: []factstore.Clause{
			&factstore.Pattern{Entity: factstore.Var("ev"), Attribute: ":event/entity", Value: factstore.Lit(int64(eid))},
			&factstore.Pattern{Entity: factstore.Var("ev"), Attribute: ":event/name", Value: factstore.Var("name")},
			&factstore.Pattern{Entity: factstore.Var("ev"), Attribute: ":event/at", Value: factstore.Var("at")},
		},
	}
	rows, err := e.Client.Query(ctx, q)
	if err != nil {
		return nil, err
	}
	out := make([]eventRecord, 0, len(rows))
	for _, r := range rows {
		if len(r) < 3 {
			continue
		}
		name, _ := r[1].(string)
		at, _ := toFloatEvent(r[2])
		out = append(out, eventRecord{name: name, at: at})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].at < out[j].at })
	return out, nil
}

// matchesSequence reports whether events contain the given step
// names in order, with the elapsed time from the first match to
// the last bounded by windowSeconds (0 = no upper bound).
func matchesSequence(events []eventRecord, steps []string, windowSeconds float64) bool {
	if len(steps) == 0 {
		return true
	}
	// Two-pointer scan: for each starting position where the first
	// step matches, greedily walk forward looking for each
	// subsequent step. Window applies to the (first, last) span.
	for start := 0; start < len(events); start++ {
		if events[start].name != steps[0] {
			continue
		}
		matched := 1
		lastAt := events[start].at
		for j := start + 1; j < len(events) && matched < len(steps); j++ {
			if events[j].name == steps[matched] {
				matched++
				lastAt = events[j].at
			}
		}
		if matched == len(steps) {
			if windowSeconds <= 0 || (lastAt-events[start].at) <= windowSeconds {
				return true
			}
		}
	}
	return false
}

func toFloatEvent(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int32:
		return float64(n), true
	case int64:
		return float64(n), true
	case time.Time:
		return float64(n.Unix()), true
	}
	return 0, false
}
