package planner

import "testing"

// TestPlanCalculateMethods: avg/sum/count emit an aggregate FactQuery bound to
// the calc name; wma emits a reduced FactQuery; having emits a Filter.
func TestPlanCalculateMethods(t *testing.T) {
	p := planBlock(t, `
detect "Rates" {
  for records where type == "machine"
  calculate mean_x from records where type == "reading" of attr "x" average
  calculate n from records where type == "reading" count
  calculate rate from activities of attr "amount" weighted_moving_average last 7 days
  having mean_x > 1
  flag matching items
  label "{mean_x}"
  priority HIGH
}`, "Rates")

	var meanX, count, wma *FactQuery
	haveFilter := false
	for _, step := range p.Steps {
		switch s := step.(type) {
		case *FactQuery:
			switch s.Into {
			case "mean_x":
				meanX = s
			case "n":
				count = s
			case "rate":
				wma = s
			}
		case *Filter:
			haveFilter = true
		}
	}

	if meanX == nil || len(meanX.Query.Aggregates) != 1 || meanX.Query.Aggregates[0].Fn != "avg" {
		t.Fatalf("mean_x: want an avg aggregate FactQuery, got %+v", meanX)
	}
	if meanX.Query.Aggregates[0].As != "mean_x" {
		t.Errorf("aggregate As = %q, want mean_x", meanX.Query.Aggregates[0].As)
	}
	if count == nil || len(count.Query.Aggregates) != 1 || count.Query.Aggregates[0].Fn != "count" {
		t.Fatalf("n: want a count aggregate FactQuery, got %+v", count)
	}
	if wma == nil || wma.Reduce != "wma" {
		t.Fatalf("rate: want a reduced FactQuery (Reduce==\"wma\"), got %+v", wma)
	}
	if !haveFilter {
		t.Error("having: expected a Filter step")
	}
}
