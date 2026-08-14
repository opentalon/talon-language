package testrunner

import (
	"sort"

	"github.com/opentalon/tln-language/internal/planner"
)

// narrowByRecordSequence is the testrunner's analogue of
// executor.execRecordSequence. It runs against the in-memory entity
// map rather than going through a FactStore, but the matching
// semantics are identical: an entity is kept only when its grouping
// records (those whose `:record/<On>` points at it) contain the
// required type sequence in order with the first→last span bounded by
// WindowSeconds (0 = unbounded).
func narrowByRecordSequence(s *planner.RecordSequenceStep, flagged []int, entities map[int]*entity) []int {
	if len(s.Steps) == 0 || len(flagged) == 0 {
		return flagged
	}
	groupAttr := ":record/" + s.On
	// Bucket every record by its grouping target id once, instead of
	// re-scanning entities per candidate.
	byTarget := map[int][]recordSeqEntry{}
	for id, ent := range entities {
		typ, ok := ent.fields[":record/type"].(string)
		if !ok {
			continue
		}
		target, ok := toEntityID(ent.fields[groupAttr])
		if !ok {
			continue
		}
		at, ok := recordAt(ent.fields[":record/at"])
		if !ok {
			// Record has no timestamp — skip; matchesSequence needs ordering.
			_ = id
			continue
		}
		byTarget[target] = append(byTarget[target], recordSeqEntry{typ: typ, at: at})
	}
	for k := range byTarget {
		sort.Slice(byTarget[k], func(i, j int) bool { return byTarget[k][i].at < byTarget[k][j].at })
	}
	kept := flagged[:0]
	for _, id := range flagged {
		records := byTarget[id]
		if matchesRecordSequence(records, s.Steps, s.WindowSeconds) {
			kept = append(kept, id)
		}
	}
	return kept
}

// recordSeqEntry is the typed pair the matcher consumes — one record's
// type plus its :record/at timestamp.
type recordSeqEntry struct {
	typ string
	at  float64
}

// matchesRecordSequence is identical in shape to
// executor.matchesSequence; duplicated here to avoid an import cycle
// (testrunner doesn't import executor).
func matchesRecordSequence(records []recordSeqEntry, steps []string, windowSeconds float64) bool {
	if len(steps) == 0 {
		return true
	}
	for start := 0; start < len(records); start++ {
		if records[start].typ != steps[0] {
			continue
		}
		matched := 1
		lastAt := records[start].at
		for j := start + 1; j < len(records) && matched < len(steps); j++ {
			if records[j].typ == steps[matched] {
				matched++
				lastAt = records[j].at
			}
		}
		if matched == len(steps) {
			if windowSeconds <= 0 || (lastAt-records[start].at) <= windowSeconds {
				return true
			}
		}
	}
	return false
}

func recordAt(v any) (float64, bool) {
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
	}
	return 0, false
}
