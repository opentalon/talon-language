package validator

import (
	"strings"
	"testing"

	"github.com/opentalon/tln-language/internal/diagnostic"
	"github.com/opentalon/tln-language/internal/lexer"
	"github.com/opentalon/tln-language/internal/parser"
)

func pipeline(t *testing.T, src string) diagnostic.List {
	t.Helper()
	tokens, ld := lexer.Lex("test.tln", src)
	if ld.HasErrors() {
		t.Fatalf("lex errors: %v", ld)
	}
	prog, pd := parser.Parse("test.tln", tokens)
	if pd.HasErrors() {
		t.Fatalf("parse errors: %v", pd)
	}
	return Validate("test.tln", prog)
}

func mustClean(t *testing.T, src string) {
	t.Helper()
	diags := pipeline(t, src)
	if diags.HasErrors() {
		t.Fatalf("expected no errors, got:\n%v", diags)
	}
}

func mustError(t *testing.T, src string, containing string) {
	t.Helper()
	diags := pipeline(t, src)
	if !diags.HasErrors() {
		t.Fatalf("expected error containing %q, got no errors", containing)
	}
	for _, d := range diags {
		if strings.Contains(d.Message, containing) || strings.Contains(d.Hint, containing) {
			return
		}
	}
	t.Fatalf("expected error containing %q, got:\n%v", containing, diags)
}

// ─── Valid programs ────────────────────────────────────────────────────────────

func TestValidateEmpty(t *testing.T) {
	mustClean(t, "")
}

func TestValidateCompleteDetect(t *testing.T) {
	mustClean(t, `
detect "Low stock" {
  for records where type == "item"
  flag matching items
  label "Low stock"
  priority HIGH
}`)
}

func TestValidateCompleteRule(t *testing.T) {
	mustClean(t, `
rule "No assign" {
  for records where type == "item"
  block "assign"
  reason "Cannot assign"
}`)
}

func TestValidateCompleteRecommend(t *testing.T) {
	mustClean(t, `
detect "Low stock" {
  for records where type == "item"
  flag matching items
}
recommend "Order more" {
  when detect "Low stock" matches
  suggest "Order more"
}`)
}

func TestValidateCompletePredict(t *testing.T) {
	mustClean(t, `
predict "Failure risk" {
  for records where type == "item"
  features [attr "km", attr "age"]
  trained_on records where type == "item" and status == "retired"
  label_attr "outcome"
  label "Risk"
}`)
}

func TestValidateCompleteForecast(t *testing.T) {
	mustClean(t, `
forecast "Stock out" {
  for records where type == "item"
  series attr "stock" over last 30 days
  label "Stock out soon"
}`)
}

func TestValidateValidDefineRef(t *testing.T) {
	mustClean(t, `
define "high_value" {
  attr "price" > 10000
}
detect "Expensive items" {
  for records where is "high_value"
  flag matching items
}`)
}

func TestValidateValidDetectRef(t *testing.T) {
	mustClean(t, `
detect "Low stock" {
  for records where type == "item"
  flag matching items
}
recommend "Reorder" {
  when detect "Low stock" matches
  suggest "Place order"
}`)
}

// ─── Completeness ──────────────────────────────────────────────────────────────

func TestValidateDetectMissingFlag(t *testing.T) {
	mustError(t, `
detect "Low stock" {
  for records where type == "item"
  label "Low"
  priority HIGH
}`, "requires a 'flag' clause")
}

func TestValidateRuleMissingAction(t *testing.T) {
	mustError(t, `
rule "No assign" {
  for records where type == "item"
  reason "Cannot assign"
}`, "requires a 'block', 'allow', 'requires', or 'do' clause")
}

// A `do` clause alone satisfies the rule-needs-an-action check: a rule that
// hands the host actions has an outcome, even without a verdict clause.
func TestValidateRuleDoOnlyIsEnough(t *testing.T) {
	mustClean(t, `
rule "Comment only" {
  for records where type == "pr"
  do comment "pr" "noted"
}`)
}

func TestValidateRecommendMissingSuggest(t *testing.T) {
	mustError(t, `
recommend "Order" {
  when detect "Low stock" matches
  priority HIGH
}`, "requires a 'suggest' clause")
}

func TestValidatePredictMissingFeatures(t *testing.T) {
	mustError(t, `
predict "Failure" {
  for records where type == "item"
  label "Risk"
}`, "requires a 'features' clause")
}

func TestValidateForecastMissingSeries(t *testing.T) {
	mustError(t, `
forecast "Stock out" {
  for records where type == "item"
  label "Out soon"
}`, "requires a 'series' clause")
}

// ─── Reference resolution ──────────────────────────────────────────────────────

func TestValidateUndefinedDefineRef(t *testing.T) {
	mustError(t, `
detect "Check" {
  for records where is "nonexistent_define"
  flag matching items
}`, "undefined define reference")
}

func TestValidateUndefinedDefineRefWithSuggestion(t *testing.T) {
	diags := pipeline(t, `
define "high_value" {
  attr "price" > 10000
}
detect "Check" {
  for records where is "high_valeu"
  flag matching items
}`)
	if !diags.HasErrors() {
		t.Fatal("expected error")
	}
	var found bool
	for _, d := range diags {
		if strings.Contains(d.Hint, "high_value") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected suggestion 'high_value' in hint, got:\n%v", diags)
	}
}

func TestValidateUndefinedDetectRef(t *testing.T) {
	mustError(t, `
recommend "Order" {
  when detect "nonexistent_detect" matches
  suggest "Order more"
}`, "undefined block reference")
}

func TestValidateOnBlockWorkflowRef(t *testing.T) {
	mustClean(t, `
on change attr "current_stock" to 0 {
  workflow "Refill stock"
}
workflow "Refill stock" {
  step "order" {
    mcp "timly" "create-order" { quantity 50 }
  }
}`)
}

func TestValidateOnBlockUndefinedWorkflowRef(t *testing.T) {
	mustError(t, `
on change attr "current_stock" to 0 {
  workflow "Refill stock"
}`, `references undefined block "Refill stock"`)
}

// ─── Duplicate names ───────────────────────────────────────────────────────────

func TestValidateDuplicateBlockNames(t *testing.T) {
	mustError(t, `
detect "Low stock" {
  for records where type == "item"
  flag matching items
}
detect "Low stock" {
  for records where type == "item"
  flag matching items
}`, "duplicate block name")
}

// ─── Cycle detection ───────────────────────────────────────────────────────────

func TestValidateCyclicDefines(t *testing.T) {
	mustError(t, `
define "a" {
  attr "x" is "b"
}
define "b" {
  is "a"
}`, "circular dependency")
}

func TestValidateNoCycleWithLinearChain(t *testing.T) {
	mustClean(t, `
define "base" {
  attr "price" > 0
}
define "expensive" {
  attr "price" is "base"
}`)
}

// ─── Type checking ─────────────────────────────────────────────────────────────

func TestValidateTypeWarningStringComparedWithGt(t *testing.T) {
	diags := pipeline(t, `
detect "Check" {
  for records where attr "name" > "hello"
  flag matching items
}`)
	var hasWarn bool
	for _, d := range diags {
		if d.Severity == diagnostic.Warning && strings.Contains(d.Message, "string") {
			hasWarn = true
		}
	}
	if !hasWarn {
		t.Error("expected type warning for string comparison with >")
	}
}

// ─── Workflow validation ────────────────────────────────────────────────────────

func TestValidateWorkflowClean(t *testing.T) {
	mustClean(t, `
workflow "Onboard" {
  step "create" {
    mcp "hr" "create-person" {
      name "Alice"
    }
  }
  step "assign" depends_on "create" {
    mcp "inv" "assign-item" {
      person_id step("create").result.id
    }
  }
}`)
}

func TestValidateWorkflowDuplicateStep(t *testing.T) {
	mustError(t, `
workflow "Dup" {
  step "a" {
    mcp "s" "t" { x 1 }
  }
  step "a" {
    mcp "s" "t" { y 2 }
  }
}`, "duplicate step name")
}

func TestValidateWorkflowUndefinedDep(t *testing.T) {
	mustError(t, `
workflow "Bad dep" {
  step "a" depends_on "missing" {
    mcp "s" "t" { x 1 }
  }
}`, "undefined step")
}

func TestValidateWorkflowCycle(t *testing.T) {
	mustError(t, `
workflow "Cycle" {
  step "a" depends_on "b" {
    mcp "s" "t" { x 1 }
  }
  step "b" depends_on "a" {
    mcp "s" "t" { x 1 }
  }
}`, "circular dependency")
}

// ─── find related (PPR) validation ────────────────────────────────────────────

func TestValidateRelatedBlockRequiresSeeds(t *testing.T) {
	mustError(t, `
find related "Bad" {
  for records where type == "item"
}`, "requires a 'to' or 'seeds [...]'")
}

func TestValidateRelatedBlockBadDamping(t *testing.T) {
	mustError(t, `
find related "Bad damping" {
  for records where type == "item"
  to attr "id"
  damping 1.0
}`, "damping must be in")
}

func TestValidateRelatedBlockBadTopK(t *testing.T) {
	mustError(t, `
find related "Bad topk" {
  for records where type == "item"
  to attr "id"
  top_k 0
}`, "top_k must be > 0")
}

func TestValidateRelatedBlockClean(t *testing.T) {
	mustClean(t, `
find related "Good" {
  for records where type == "item"
  seeds [10, 20, 30]
  top_k 5
  damping 0.85
  tolerance 0.0001
  max_iterations 50
}`)
}

// ─── Templates ───────────────────────────────────────────────────────────────

func TestValidateTemplateUnknownFunction(t *testing.T) {
	mustError(t, `
detect "Has unknown fn" {
  for records where type == "item"
  flag matching items
  label "{wat(attr.x)} items"
}`, "unknown function")
}

func TestValidateTemplateKnownFunctionsClean(t *testing.T) {
	mustClean(t, `
detect "Known fns" {
  for records where type == "item"
  flag matching items
  label "{count} items, avg {avg(attr.km)} km, max {max(attr.km)}; expires in {days_until(attr.expires_at)} days"
}`)
}

func TestValidateScoreOutOfRange(t *testing.T) {
	mustError(t, `
detect "Bad score" {
  for records where type == "item"
  flag matching items
  confidence 1.5
}`, "outside [0, 1]")
}

func TestValidateScoreInRangeClean(t *testing.T) {
	mustClean(t, `
detect "Good score" {
  for records where type == "item"
  flag matching items
  confidence 0.82
  source "auto-discovered"
}`)
}


func TestValidateRemediateEmpty(t *testing.T) {
	mustError(t, `
detect "Act" {
  for records where status == "defective"
  flag matching items
  remediate {
  }
}`, "requires at least one mcp call")
}

func TestValidateDetectRemediateOK(t *testing.T) {
	mustClean(t, `
detect "Act" {
  for records where status == "defective"
  flag matching items
  remediate {
    mcp "inventory" "create-ticket" { title "x" }
  }
}`)
}

func TestValidateEnrichOK(t *testing.T) {
	mustClean(t, `
enrich "R" {
  for records where type == "stock_item"
  stale_after 1 hour
  mcp "inv" "show" { id attr "id" }
  update attr "current_stock" from result.current_stock
}`)
}

func TestValidateEnrichRequiresUpdate(t *testing.T) {
	mustError(t, `
enrich "R" {
  for records where type == "stock_item"
  stale_after 1 hour
  mcp "inv" "show" { id attr "id" }
}`, "at least one 'update")
}

// ─── was ... ago restrictions ───────────────────────────────────────────────

func TestValidateWasAgoTopLevelOK(t *testing.T) {
	mustClean(t, `
detect "Regressed" {
  for records where type == "machine"
    and was (attr "status" == "certified") 90 days ago
  flag matching items
}`)
}

func TestValidateWasAgoNestedInOrRejected(t *testing.T) {
	mustError(t, `
detect "Bad" {
  for records where type == "machine"
    or was (attr "status" == "certified") 90 days ago
  flag matching items
}`, "top-level `and`")
}

func TestValidateCorrelationThresholdInRange(t *testing.T) {
	mustClean(t, `
detect "Corr OK" {
  for records where type == "vehicle"
    and attr "km" correlates_with attr "failure_count" over last 90 days > 0.7
  flag matching items
}`)
}

func TestValidateCorrelationThresholdOutOfRange(t *testing.T) {
	mustError(t, `
detect "Corr bad" {
  for records where type == "vehicle"
    and attr "km" correlates_with attr "failure_count" over last 90 days > 1.5
  flag matching items
}`, "outside [-1, 1]")
}

func TestValidateCalculateCountNeedsNoValue(t *testing.T) {
	mustClean(t, `
detect "C" {
  for records where type == "s"
  calculate n from records where type == "reading" count
  having n > 0
  flag matching items
}`)
}

func TestValidateCalculateAvgRequiresValue(t *testing.T) {
	mustError(t, `
detect "C" {
  for records where type == "s"
  calculate r from records average
  flag matching items
}`, "requires a value column")
}
