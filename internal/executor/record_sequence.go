package executor

import (
	"context"
	"sort"

	"github.com/opentalon/tln-language/internal/factstore"
	"github.com/opentalon/tln-language/internal/planner"
)

// execRecordSequence keeps Input rows whose entity (the grouping
// target, typically an item) has the ordered set of records described
// by Steps within WindowSeconds. Each record is its own EAV entity:
//
//	:record/type  one of Steps (e.g. "electrical_fault")
//	:record/<On>  foreign key onto the candidate (e.g. ":record/item" = 1)
//	:record/at    timestamp in seconds
//
// The match contract mirrors execEventSequence: the matcher walks
// records sorted by :record/at and looks for an in-order subsequence
// of types where last.at − first.at ≤ WindowSeconds. WindowSeconds of
// 0 is "unbounded".
//
// We deliberately reuse the EventSequence semantics here rather than
// inventing a new automaton — record sequences are the same shape as
// event sequences with a different schema. The talondb adapter has a
// server-side SequenceJoin RPC that could push this work to the
// server in a future change; for now the FactStore path keeps the
// in-memory and remote backends symmetrical.
func (e *Executor) execRecordSequence(ctx context.Context, s *planner.RecordSequenceStep, vars map[string]any) (StepResult, error) {
	rows, _ := vars[s.Input].([][]any)
	if len(s.Steps) == 0 || len(rows) == 0 {
		vars[s.Into] = rows
		return StepResult{Type: "RecordSequenceStep", Name: s.BlockName, Output: rows}, nil
	}

	kept := make([][]any, 0, len(rows))
	for _, row := range rows {
		eid, ok := toIntSM(row[0])
		if !ok {
			continue
		}
		records, err := e.recordsForGroup(ctx, eid, s.On)
		if err != nil {
			return StepResult{}, err
		}
		if matchesSequence(records, s.Steps, s.WindowSeconds) {
			kept = append(kept, row)
		}
	}
	vars[s.Into] = kept
	return StepResult{
		Type:   "RecordSequenceStep",
		Name:   s.BlockName,
		Output: kept,
	}, nil
}

// recordsForGroup returns every record entity whose `:record/<on>`
// attribute references eid, projected into the same eventRecord
// shape execEventSequence uses. Sorted by :record/at ascending so
// matchesSequence sees the natural time order.
func (e *Executor) recordsForGroup(ctx context.Context, eid int, on string) ([]eventRecord, error) {
	attr := ":record/" + on
	q := factstore.Query{
		Find: []string{"?r", "?type", "?at"},
		Where: []factstore.Clause{
			&factstore.Pattern{Entity: factstore.Var("r"), Attribute: attr, Value: factstore.Lit(int64(eid))},
			&factstore.Pattern{Entity: factstore.Var("r"), Attribute: ":record/type", Value: factstore.Var("type")},
			&factstore.Pattern{Entity: factstore.Var("r"), Attribute: ":record/at", Value: factstore.Var("at")},
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
		typ, _ := r[1].(string)
		at, _ := toFloatEvent(r[2])
		out = append(out, eventRecord{name: typ, at: at})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].at < out[j].at })
	return out, nil
}
