package talondb

import (
	"fmt"
	"sort"

	"github.com/opentalon/tln-language/internal/factstore"
)

// runAggregates groups matched binding sets by GroupBy and computes
// each Aggregate. Result row layout is [group-by columns..., aggregate
// columns...] — matches MemoryStore's runAggregates for parity with
// the in-memory backend. Group order is the lexicographic order of
// the stringified group key so results are deterministic.
//
// This is the Go-side fallback path. A future PR can push specific
// shapes (e.g., aggregate with no GroupBy over a single attr) into
// talon-db's Stats RPC; for now everything goes through this routine.
func runAggregates(matches []map[string]any, groupBy []string, aggs []factstore.Aggregate) [][]any {
	if len(groupBy) == 0 {
		row := make([]any, len(aggs))
		for i, a := range aggs {
			row[i] = computeAggregate(a, matches)
		}
		return [][]any{row}
	}

	type bucket struct {
		key     []any
		members []map[string]any
	}
	buckets := map[string]*bucket{}
	var order []string
	for _, b := range matches {
		key := make([]any, len(groupBy))
		for i, v := range groupBy {
			key[i] = b[v]
		}
		k := aggGroupKey(key)
		if _, ok := buckets[k]; !ok {
			buckets[k] = &bucket{key: key}
			order = append(order, k)
		}
		buckets[k].members = append(buckets[k].members, b)
	}
	sort.Strings(order)

	rows := make([][]any, 0, len(order))
	for _, k := range order {
		bk := buckets[k]
		row := make([]any, 0, len(groupBy)+len(aggs))
		row = append(row, bk.key...)
		for _, a := range aggs {
			row = append(row, computeAggregate(a, bk.members))
		}
		rows = append(rows, row)
	}
	return rows
}

func computeAggregate(a factstore.Aggregate, members []map[string]any) any {
	switch a.Fn {
	case "count":
		return float64(len(members))
	case "sum", "total":
		s, _ := aggSumOver(a.Over, members)
		return s
	case "avg":
		s, n := aggSumOver(a.Over, members)
		if n == 0 {
			return float64(0)
		}
		return s / float64(n)
	case "min":
		return aggMinOver(a.Over, members)
	case "max":
		return aggMaxOver(a.Over, members)
	}
	return nil
}

func aggSumOver(t factstore.Term, members []map[string]any) (float64, int) {
	if t.Var == "" {
		return 0, 0
	}
	var sum float64
	n := 0
	for _, b := range members {
		if f, ok := aggFloat(b[t.Var]); ok {
			sum += f
			n++
		}
	}
	return sum, n
}

func aggMinOver(t factstore.Term, members []map[string]any) any {
	if t.Var == "" {
		return nil
	}
	var best float64
	seen := false
	for _, b := range members {
		f, ok := aggFloat(b[t.Var])
		if !ok {
			continue
		}
		if !seen || f < best {
			best = f
			seen = true
		}
	}
	if !seen {
		return nil
	}
	return best
}

func aggMaxOver(t factstore.Term, members []map[string]any) any {
	if t.Var == "" {
		return nil
	}
	var best float64
	seen := false
	for _, b := range members {
		f, ok := aggFloat(b[t.Var])
		if !ok {
			continue
		}
		if !seen || f > best {
			best = f
			seen = true
		}
	}
	if !seen {
		return nil
	}
	return best
}

func aggFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case float32:
		return float64(x), true
	case int:
		return float64(x), true
	case int32:
		return float64(x), true
	case int64:
		return float64(x), true
	}
	return 0, false
}

// aggGroupKey stringifies a composite group-by key so map lookups
// don't depend on slice equality. The %v format is good enough for
// the tln-language planner's group-by vocabulary (strings, numbers,
// bools); nested values shouldn't appear.
func aggGroupKey(parts []any) string {
	var b []byte
	for i, p := range parts {
		if i > 0 {
			b = append(b, '|')
		}
		b = append(b, fmt.Sprintf("%v", p)...)
	}
	return string(b)
}
