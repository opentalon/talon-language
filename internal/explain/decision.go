// Package explain produces user-facing "why did this fire" explanations
// for tln decisions. Tier 1 surfaces the rendered label, the conditions
// that satisfied the rule, and the observed fact values behind each one.
//
// Persistence, temporal replay, and counter-factual queries are out of
// scope at this tier — see docs/design/0003-explainability.md for the
// roadmap.
package explain

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Decision is one block's firing — what happened, why, and on what evidence.
// Tier-1 explanations are constructed from values already in memory at
// evaluation time; no FactStore time-travel is required.
type Decision struct {
	// Identity
	BlockName  string    `json:"block"`
	BlockKind  string    `json:"kind"`             // "detect", "recommend", "forecast", "rule", …
	BlockFile  string    `json:"file,omitempty"`   // source location for audit view
	BlockLine  int       `json:"line,omitempty"`
	EntityID   int       `json:"entity_id"`
	EntityName string    `json:"entity_name,omitempty"`
	FiredAt    time.Time `json:"fired_at"`

	// What the user sees
	Action   string   `json:"action,omitempty"`    // rendered label / suggest
	Why      []string `json:"why,omitempty"`       // bullet reasons, one per fired condition
	Evidence []Fact   `json:"evidence,omitempty"`  // (attr, value, observed_at)

	// Cross-block chain
	TriggeredBy []Decision `json:"triggered_by,omitempty"` // upstream decisions
	Confidence  string     `json:"confidence,omitempty"`   // "High", "Medium", "Low", or ""
	Priority    string     `json:"priority,omitempty"`     // CRITICAL/HIGH/MEDIUM/LOW

	// Provenance — populated from the block's `confidence N` /
	// `source "..."` annotations (issue #3 layer-3). Score is the
	// rule's self-asserted confidence in [0, 1]; Source is opaque
	// metadata, typically describing how the rule was discovered
	// (e.g. "mined from 14 months of data, 47 matching cases").
	Score  *float64 `json:"score,omitempty"`
	Source string   `json:"source,omitempty"`
}

// Fact is one piece of evidence cited in a decision. ObservedAt is the
// time the planner *read* the value — until talon-db exposes per-datom
// transaction times, this is wall-clock at evaluation, not true fact
// observation time. The Source field is similarly best-effort.
type Fact struct {
	Attribute  string    `json:"attribute"`
	Value      any       `json:"value"`
	ObservedAt time.Time `json:"observed_at,omitempty"`
	Source     string    `json:"source,omitempty"`
}

// Render formats a Decision in the Tier-1 end-user view.
//
//	─────────────────────────────────────────────────────────────────
//	ACTION    Order 100 bags of Portland Cement 50kg
//	ITEM      Portland Cement 50kg  (stock item #808)
//	WHEN      Recommended 2026-05-27 09:14 UTC
//
//	WHY
//	  • Stock is critically low — 12 bags on hand, minimum is 50.
//	  • …
//
//	EVIDENCE
//	  Observed 2026-05-27 08:00  current_stock = 12
//	  …
//
//	CONFIDENCE  High
//	─────────────────────────────────────────────────────────────────
func Render(d Decision) string {
	var b strings.Builder
	const rule = "─────────────────────────────────────────────────────────────────\n"

	b.WriteString(rule)
	if d.Action != "" {
		fmt.Fprintf(&b, "ACTION    %s\n", d.Action)
	} else {
		fmt.Fprintf(&b, "%-10s%s — %s\n", strings.ToUpper(d.BlockKind), d.BlockName, summary(d))
	}
	if d.EntityName != "" {
		fmt.Fprintf(&b, "ITEM      %s  (entity #%d)\n", d.EntityName, d.EntityID)
	} else if d.EntityID != 0 {
		fmt.Fprintf(&b, "ITEM      entity #%d\n", d.EntityID)
	}
	if !d.FiredAt.IsZero() {
		fmt.Fprintf(&b, "WHEN      %s %s\n",
			capitalize(d.BlockKind),
			d.FiredAt.UTC().Format("2006-01-02 15:04 MST"))
	}
	if d.Priority != "" {
		fmt.Fprintf(&b, "PRIORITY  %s\n", d.Priority)
	}

	why := collectWhy(d)
	if len(why) > 0 {
		b.WriteString("\nWHY\n")
		for _, line := range why {
			fmt.Fprintf(&b, "  • %s\n", line)
		}
	}

	ev := collectEvidence(d)
	if len(ev) > 0 {
		b.WriteString("\nEVIDENCE\n")
		for _, f := range ev {
			ts := ""
			if !f.ObservedAt.IsZero() {
				ts = f.ObservedAt.UTC().Format("Observed 2006-01-02 15:04  ")
			}
			fmt.Fprintf(&b, "  %s%s = %v\n", ts, f.Attribute, f.Value)
		}
	}

	if d.Confidence != "" {
		fmt.Fprintf(&b, "\nCONFIDENCE   %s\n", d.Confidence)
	}
	if d.Score != nil {
		fmt.Fprintf(&b, "SCORE        %.2f\n", *d.Score)
	}
	if d.Source != "" {
		fmt.Fprintf(&b, "SOURCE       %s\n", d.Source)
	}
	b.WriteString(rule)
	return b.String()
}

// RenderAll concatenates the Tier-1 view of every decision, with a
// trailing newline between blocks.
func RenderAll(ds []Decision) string {
	var b strings.Builder
	for i, d := range ds {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(Render(d))
	}
	return b.String()
}

// collectWhy walks the decision tree (recommend → detect → forecast…)
// and gathers every block's "why" lines in firing order. Dedupes
// adjacent duplicates so chained explanations stay readable.
func collectWhy(d Decision) []string {
	var out []string
	for _, up := range d.TriggeredBy {
		out = append(out, collectWhy(up)...)
	}
	for _, w := range d.Why {
		if len(out) == 0 || out[len(out)-1] != w {
			out = append(out, w)
		}
	}
	return out
}

// collectEvidence dedupes facts across the chain by (Attribute, Value)
// so the same observation isn't cited twice when multiple upstream
// blocks reference it.
func collectEvidence(d Decision) []Fact {
	seen := map[string]struct{}{}
	var out []Fact
	var walk func(Decision)
	walk = func(x Decision) {
		for _, up := range x.TriggeredBy {
			walk(up)
		}
		for _, f := range x.Evidence {
			key := fmt.Sprintf("%s=%v", f.Attribute, f.Value)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, f)
		}
	}
	walk(d)
	// Stable order: by attribute name then string value.
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Attribute != out[j].Attribute {
			return out[i].Attribute < out[j].Attribute
		}
		return fmt.Sprintf("%v", out[i].Value) < fmt.Sprintf("%v", out[j].Value)
	})
	return out
}

func summary(d Decision) string {
	if len(d.Why) > 0 {
		return d.Why[0]
	}
	return "fired"
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
