package explain

import (
	"strings"
	"testing"
	"time"
)

func TestRenderTier1MinimalDecision(t *testing.T) {
	d := Decision{
		BlockName:  "Cement running low",
		BlockKind:  "detect",
		EntityID:   808,
		EntityName: "Portland Cement 50kg",
		FiredAt:    time.Date(2026, 5, 27, 9, 14, 0, 0, time.UTC),
		Action:     "Order 100 bags of Portland Cement 50kg",
		Why: []string{
			"Stock is critically low — 12 bags on hand, minimum is 50.",
			"Forecast says stock hits zero in 3 days at current usage.",
		},
		Evidence: []Fact{
			{Attribute: "current_stock", Value: 12,
				ObservedAt: time.Date(2026, 5, 27, 8, 0, 0, 0, time.UTC)},
			{Attribute: "minimum_amount", Value: 50,
				ObservedAt: time.Date(2026, 5, 27, 8, 0, 0, 0, time.UTC)},
		},
		Confidence: "High",
		Priority:   "CRITICAL",
	}
	out := Render(d)

	for _, want := range []string{
		"ACTION    Order 100 bags",
		"ITEM      Portland Cement 50kg  (entity #808)",
		"PRIORITY  CRITICAL",
		"WHY",
		"• Stock is critically low",
		"EVIDENCE",
		"current_stock = 12",
		"minimum_amount = 50",
		"CONFIDENCE   High",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered output missing %q. Got:\n%s", want, out)
		}
	}
}

func TestCollectWhyChainsThroughTriggeredBy(t *testing.T) {
	d := Decision{
		BlockName: "Order cement",
		BlockKind: "recommend",
		TriggeredBy: []Decision{
			{
				BlockName: "Cement running low",
				BlockKind: "detect",
				Why:       []string{"Stock is below minimum"},
			},
			{
				BlockName: "Cement stock-out date",
				BlockKind: "forecast",
				Why:       []string{"Forecast: zero in 3 days"},
			},
		},
		Why: []string{"Order 100 bags to cover 4 weeks"},
	}
	got := collectWhy(d)
	want := []string{
		"Stock is below minimum",
		"Forecast: zero in 3 days",
		"Order 100 bags to cover 4 weeks",
	}
	if len(got) != len(want) {
		t.Fatalf("got %d why lines, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("why[%d]: got %q, want %q", i, got[i], want[i])
		}
	}
}

func TestCollectEvidenceDedupesAcrossChain(t *testing.T) {
	d := Decision{
		BlockName: "recommend",
		TriggeredBy: []Decision{
			{
				BlockName: "detect",
				Evidence: []Fact{
					{Attribute: "current_stock", Value: 12},
					{Attribute: "minimum_amount", Value: 50},
				},
			},
		},
		Evidence: []Fact{
			{Attribute: "current_stock", Value: 12}, // duplicate — same attr+value
			{Attribute: "weekly_consumption", Value: 28},
		},
	}
	got := collectEvidence(d)
	if len(got) != 3 {
		t.Fatalf("got %d facts, want 3 (deduped): %+v", len(got), got)
	}
	// Sorted by attribute then value.
	wantAttrs := []string{"current_stock", "minimum_amount", "weekly_consumption"}
	for i, w := range wantAttrs {
		if got[i].Attribute != w {
			t.Errorf("evidence[%d]: got %q, want %q", i, got[i].Attribute, w)
		}
	}
}

func TestRenderChainedDecisionMergesWhy(t *testing.T) {
	d := Decision{
		BlockName: "Order cement",
		BlockKind: "recommend",
		EntityID:  808,
		Action:    "Order 100 bags",
		FiredAt:   time.Date(2026, 5, 27, 9, 14, 0, 0, time.UTC),
		TriggeredBy: []Decision{
			{
				BlockName: "Cement running low",
				BlockKind: "detect",
				Why:       []string{"Stock 12 ≤ minimum 50"},
				Evidence: []Fact{
					{Attribute: "current_stock", Value: 12},
				},
			},
		},
		Why: []string{"4 weeks of cover at 28/week = 100 bags after stock"},
	}
	out := Render(d)
	// Both the upstream detect's reasoning and the recommend's own
	// reasoning should appear in the rendered output.
	for _, want := range []string{
		"Stock 12 ≤ minimum 50",
		"4 weeks of cover",
		"current_stock = 12",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q. Got:\n%s", want, out)
		}
	}
}

func TestRenderEmptyDecisionStillReadable(t *testing.T) {
	out := Render(Decision{BlockName: "X", BlockKind: "detect"})
	if !strings.Contains(out, "X") {
		t.Errorf("empty decision should still surface block name. Got:\n%s", out)
	}
}
