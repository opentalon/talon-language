package executor

import "testing"

// step("find").result.N.field navigates into a LIST step result (find-by-name
// → use the first match's id), alongside plain map navigation.
func TestResolveStepField_ListIndex(t *testing.T) {
	vars := map[string]any{
		"find_result": map[string]any{
			"result": []any{
				map[string]any{"id": 124871.0, "name": "Wittorf IT-Büro Lagerplatz 01"},
				map[string]any{"id": 999.0, "name": "other"},
			},
		},
	}

	if got := resolveStepField(vars, "find", "result.0.id"); got != 124871.0 {
		t.Fatalf("result.0.id: got %v, want 124871", got)
	}
	if got := resolveStepField(vars, "find", "result.1.name"); got != "other" {
		t.Fatalf("result.1.name: got %v, want other", got)
	}
	// Out-of-range and non-list navigation degrade to nil, not a panic.
	if got := resolveStepField(vars, "find", "result.9.id"); got != nil {
		t.Fatalf("out-of-range index: got %v, want nil", got)
	}
	if got := resolveStepField(vars, "find", "result.0.id.nope"); got != nil {
		t.Fatalf("navigating past a scalar: got %v, want nil", got)
	}
}
