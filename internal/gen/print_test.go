package gen

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/opentalon/talon-language/internal/ast"
	"github.com/opentalon/talon-language/internal/lexer"
	"github.com/opentalon/talon-language/internal/parser"
)

// parseProg lex+parses src, failing the test on any diagnostic. The printer's
// whole contract is that its output parses cleanly, so a parse error here is a
// printer bug, not a test-input bug.
func parseProg(t *testing.T, label, src string) *ast.Program {
	t.Helper()
	tokens, ld := lexer.Lex(label, src)
	if ld.HasErrors() {
		t.Fatalf("%s: lex errors:\n%v\n--- source ---\n%s", label, ld, src)
	}
	prog, pd := parser.Parse(label, tokens)
	if pd.HasErrors() {
		t.Fatalf("%s: parse errors:\n%v\n--- source ---\n%s", label, pd, src)
	}
	return prog
}

// roundTrip is the printer's core invariant: parse → Print → parse yields an
// AST equivalent to the original (position-insensitive), and printing is a
// fixed point (re-printing the re-parsed AST is byte-identical). The fixed
// point catches "wrong syntax"; the AST equality catches "lossy emission".
func roundTrip(t *testing.T, label, src string) {
	t.Helper()
	ast1 := parseProg(t, label, src)

	printed := Program(ast1)
	ast2 := parseProg(t, label+" (reprinted)", printed)

	// Fixed point: the re-parsed AST must print identically.
	if reprinted := Program(ast2); reprinted != printed {
		t.Errorf("%s: printing is not idempotent\n--- first print ---\n%s\n--- second print ---\n%s",
			label, printed, reprinted)
	}

	// AST equivalence, ignoring source positions.
	clearPos(reflect.ValueOf(ast1))
	clearPos(reflect.ValueOf(ast2))
	if !reflect.DeepEqual(ast1, ast2) {
		t.Errorf("%s: AST changed across round-trip\n--- printed source ---\n%s", label, printed)
	}
}

// clearPos zeroes every ast.Pos in the tree so structural comparison ignores
// source locations (which legitimately differ after reprinting).
func clearPos(v reflect.Value) {
	switch v.Kind() {
	case reflect.Pointer, reflect.Interface:
		if !v.IsNil() {
			clearPos(v.Elem())
		}
	case reflect.Struct:
		if v.Type() == posType {
			if v.CanSet() {
				v.Set(reflect.Zero(posType))
			}
			return
		}
		for i := 0; i < v.NumField(); i++ {
			clearPos(v.Field(i))
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < v.Len(); i++ {
			clearPos(v.Index(i))
		}
	}
}

var posType = reflect.TypeOf(ast.Pos{})

// TestRoundTripExamples round-trips every shipped example program. These are
// the real, validator-clean rules users write, so they are the highest-value
// corpus for the "nothing dropped, nothing malformed" invariant.
func TestRoundTripExamples(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join("..", "..", "examples", "*.talon"))
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) == 0 {
		t.Fatal("no example .talon files found")
	}
	for _, path := range paths {
		path := path
		t.Run(filepath.Base(path), func(t *testing.T) {
			src, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			roundTrip(t, filepath.Base(path), string(src))
		})
	}
}

// TestRoundTripExampleTests round-trips the .talon.test files, exercising the
// test/given/expect/mock block shapes the example programs don't.
func TestRoundTripExampleTests(t *testing.T) {
	var paths []string
	for _, dir := range []string{"examples", "test"} {
		found, err := filepath.Glob(filepath.Join("..", "..", dir, "*.talon.test"))
		if err != nil {
			t.Fatal(err)
		}
		paths = append(paths, found...)
	}
	if len(paths) == 0 {
		t.Fatal("no .talon.test files found")
	}
	for _, path := range paths {
		path := path
		t.Run(filepath.Base(path), func(t *testing.T) {
			src, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			roundTrip(t, filepath.Base(path), string(src))
		})
	}
}

// TestRoundTripSnippets covers per-construct shapes — including ones the
// examples don't reach — so every block type, clause, condition, and
// expression the AST models is exercised at least once.
func TestRoundTripSnippets(t *testing.T) {
	cases := map[string]string{
		"detect_minimal": `
detect "Low stock" {
  for records where type == "stock_item"
  flag matching items
  label "{item.name}: low"
  priority CRITICAL
}`,
		"detect_calculate_having": `
detect "High average consumption" {
  for records where type == "depot"
  calculate avg_use from records where type == "usage" of attr "amount" average
  having avg_use > 100
  flag matching items
  label "fleet averaging {avg_use} units/day"
}`,
		"detect_calc_methods": `
detect "Method sampler" {
  for records where type == "item"
  calculate s from records of attr "amount" sum
  calculate c from records count
  calculate w from records of attr "amount" weighted_moving_average last 7 days
  flag matching items
}`,
		"detect_pattern": `
detect "Repeated faults" {
  for records where type == "item"
  when 3+ records same category within 7 days
  flag matching items
}`,
		"detect_score_source": `
detect "Mined rule" {
  for records where type == "item"
  flag matching items
  confidence 0.82
  source "mined from 2024 dataset"
}`,
		"detect_anomaly_grubbs": `
detect "Outlier" {
  for records where type == "item"
    and attr "reading" is anomaly using grubbs compared_to last 30 days
  flag matching items
}`,
		"detect_nested_ml": `
detect "Composite" {
  for records where type == "item"
  predict features [attr "hours", attr "age"] trained_on records where status == "failed" confidence >= 0.6
  cluster by attr "x", attr "y"
  find similar to attr "profile" within 3
  find related to attr "seed" top_k 5 damping 0.85
  flag matching items
}`,
		"detect_remediate_approve": `
detect "Needs cleanup" {
  for records where type == "item"
  flag matching items
  remediate approve {
    requires role "manager"
    mcp "inventory" "flag" {
      id attr "id"
    }
  }
}`,
		"rule_strict_override": `
strict rule "Expired cert blocks assignment" {
  for records where type == "person"
  before "assign"
  block "assign"
  reason "safety"
}`,
		"rule_when_overrides": `
rule "Cleanup crew can delete" {
  when tool_action contains "delete"
  overrides "Block all deletions", "Freeze window"
  allow "delete"
  priority HIGH
}`,
		"rule_every_requires": `
rule "Service approval" {
  for records where type == "item"
  every 5000 km on attr "odometer"
  requires approval from role "manager"
  reason "high mileage"
  confidence 0.5
  source "policy"
}`,
		"recommend_probability_feedback": `
recommend "Order stock" {
  when detect "Low stock" matches
  calculate avg_use from activities within last 90 days
  suggest "Order for {item.name} (~{avg_use}/wk)" with probability 0.3 learn from feedback within 30 days
  priority HIGH
}`,
		"combine_full": `
combine "Route plan" {
  for records where type == "job"
  minimize attr "distance"
  maximize attr "value"
  subject_to total(attr "load") <= 1000
  select 5 from records
  seed 42
  sequence
  coordinates attr "lat", attr "lon"
  return id, distance
  label "plan {count}"
  priority MEDIUM
}`,
		"combine_solver_linear": `
combine "Knapsack" {
  for records where type == "item"
  maximize attr "value"
  subject_to total(attr "weight") <= 50
  solver linear
  return id
}`,
		"define_and_foreach": `
define "active_vehicle"(a, b) {
  type == "item" and status == "active" and category == "Vehicles"
  for each x in attr "parts" {
    x == "ok"
  }
}`,
		"workflow_steps": `
workflow "Refill" {
  step "check" {
    mcp "inventory" "level" {
      sku attr "sku"
    }
  }
  step "order" depends_on "check" {
    mcp "supplier" "order" {
      sku attr "sku"
      qty 10
      on_error {
        retry 3 times
        log "order failed for {item.name}: {error}"
        skip
      }
    }
  }
  step "notify" depends_on ["check", "order"] {
    mcp "slack" "post" {
      text "done"
    }
  }
}`,
		"predict_block": `
predict "Failure risk" {
  for records where type == "item"
  features [attr "hours", attr "repairs"]
  trained_on records where type == "item" and status == "retired"
  label_attr "outcome"
  confidence >= 0.7
  label "{item.name}: {class}"
  priority HIGH
}`,
		"forecast_block": `
forecast "Stock out" {
  for records where type == "stock_item" and status == "active"
  series attr "current_stock" over last 90 days
  label "{item.name}: out in ~{days_until} days"
  priority CRITICAL
}`,
		"cluster_block": `
cluster "Segments" {
  for records where type == "customer"
  by attr "recency", attr "frequency", attr "monetary"
  label "segment"
}`,
		"classify_block": `
classify "Category" {
  for records where type == "ticket"
  features [attr "text_len", attr "priority_score"]
  trained_on records where type == "ticket" and status == "triaged"
  label_attr "queue"
  confidence >= 0.5
  label "routed to {class}"
}`,
		"similar_vector": `
find similar "Near matches" {
  for records where type == "doc"
  to attr "embedding"
  using vector scope "docs"
  top 20
  label "similar"
}`,
		"related_seeds": `
find related "Associated parts" {
  for records where type == "part"
  seeds [attr "a", attr "b"]
  top_k 10
  damping 0.85
  tolerance 0.001
  max_iterations 100
  label "related"
}`,
		"on_change": `
on change attr "current_stock" to 0 {
  when attr "current_stock" <= 0
  logger.warn "stock out for {item.name}"
  recommend "Order stock"
}`,
		"on_assert_retract": `
on assert activity {
  detect "Defective item"
}`,
		"constraint_membership": `
constraint "Valid status" {
  for records where type == "item"
  require attr "status" in ["active", "defective", "missing"]
  on_violation reject "invalid status"
}`,
		"constraint_quarantine": `
constraint "Sane stock" {
  for records where type == "stock_item"
  require attr "current_stock" >= 0
  on_violation quarantine "negative stock"
}`,
		"state_machine_substates": `
state_machine "Flight" {
  for records where type == "flight"
  states scheduled, in_flight/boarding, in_flight/cruising, landed
  initial scheduled
  state_attr "flight_state"
  transition scheduled -> in_flight/boarding when attr "gate" == "open"
  transition in_flight/boarding -> in_flight/cruising when attr "altitude" > 10000
  transition in_flight -> landed when attr "altitude" == 0
  invariant in landed require attr "seatbelt" == "on"
}`,
		"enrich_block": `
enrich "Refresh stock" {
  for records where type == "stock_item"
  stale_after 7 days
  mcp "inventory" "get_level" {
    sku attr "sku"
  }
  update attr "current_stock" from result.level
  update attr "reorder_point" from result.thresholds.reorder
}`,
		"collect_every": `
collect "Ingest orders" {
  schedule every 6 hours
  mcp "erp" "list_orders" {
    since attr "last_sync"
  }
  store results as order tag "erp"
}`,
		"collect_cron": `
collect "Nightly sync" {
  schedule cron "0 2 * * *"
  mcp "erp" "dump" {
    full true
  }
  store results as snapshot
}`,
		"cond_string_matches": `
detect "Text match" {
  for records where attr "title" matches phrase "out of stock"
    and attr "sku" starts_with "AB"
    and attr "note" ends_with "!"
    and attr "body" contains "urgent"
  flag matching items
}`,
		"cond_correlation": `
detect "Correlated" {
  for records where attr "temp" correlates_with attr "failures" over last 90 days > 0.7
  flag matching items
}`,
		"cond_temporal_notin": `
detect "Stale or excluded" {
  for records where attr "updated_at" older_than 90 days
    and attr "region" not in ["us", "eu"]
  flag matching items
}`,
		"cond_category_tree": `
detect "In tree" {
  for records where category in category_tree("Vehicles")
  flag matching items
}`,
		"cond_was_ago": `
detect "Was active" {
  for records where type == "item" and was (status == "active") 30 days ago
  flag matching items
}`,
		"cond_record_sequence": `
detect "Fault chain" {
  for records where record type "electrical_fault" followed_by record type "engine_failure" on same vehicle within 14 days
  flag matching items
}`,
		"cond_event_sequence": `
detect "Funnel drop" {
  for records where event_sequence "cart_opened" -> "item_added" -> "abandoned" within 7 days
  flag matching items
}`,
		"cond_not_and_is": `
detect "Negated" {
  for records where not status == "closed" and is "active_vehicle"
  flag matching items
}`,
		"cond_changed_to": `
on change attr "status" {
  when status changed_to "shipped"
  logger.info "shipped {item.id}"
}`,
		"cond_approaching_sugar": `
detect "Expiring soon" {
  for records where attr "expires_at" approaching within 30 days
  flag matching items
}`,
		"expr_arithmetic": `
detect "Arithmetic" {
  for records where attr "a" + attr "b" * 2 > 10
    and (attr "c" - attr "d") / 2 < 5
    and -attr "e" == 0
  flag matching items
}`,
		"expr_learned_threshold": `
detect "Learned" {
  for records where attr "latency" > learned_threshold p95 of attr "latency" over last 30 days
  flag matching items
}`,
		"expr_context_step_map": `
workflow "Ctx" {
  step "a" {
    mcp "s" "t" {
      who context.actor
      first step("prev").result.items.map(id)
      one step("prev").result.value
    }
  }
}`,
		"expr_bool_membership": `
detect "Flags" {
  for records where attr "active" == true and attr "n" in [1, 2, 3]
  flag matching items
}`,
		"import_header": `import "./shared.talon"
import "./more.talon"

detect "X" {
  for records where type == "item"
  flag matching items
}`,
		"test_block": `
test "flags low stock" {
  given {
    record 1 type "stock_item" status "active" current_stock 0
    attr 1 "reorder_point" 5
  }
  when detect "Low stock"
  expect {
    flagged 1
    label contains "low"
    priority == CRITICAL
  }
}`,
		"test_mock_mcp_called": `
test "orders when low" {
  given {
    record 1 type "stock_item"
  }
  mock mcp "supplier" "order" {
    returns { status "ok" id 42 }
  }
  when detect "Low stock"
  expect {
    mcp_called "supplier" "order" with { sku == "AB1" qty >= 10 }
  }
}`,
		"test_mock_fails": `
test "handles failure" {
  given {
    record 1 type "item"
  }
  mock mcp "flaky" "call" {
    fails "boom"
  }
  when detect "X"
  expect {
    not flagged 1
  }
}`,
	}
	for name, src := range cases {
		name, src := name, src
		t.Run(name, func(t *testing.T) {
			roundTrip(t, name, src)
		})
	}
}

// TestTypedEntryPoints checks the exported per-type helpers render the same
// text as the generic dispatcher, so callers can use either.
func TestTypedEntryPoints(t *testing.T) {
	prog := parseProg(t, "typed", `
detect "D" {
  for records where type == "item"
  flag matching items
}

rule "R" {
  when tool_action contains "delete"
  block "delete"
}

recommend "Rec" {
  when detect "D" matches
  suggest "do {item.name}"
}

workflow "W" {
  step "s" {
    mcp "srv" "tool" {
      x 1
    }
  }
}`)

	if got, want := Detect(prog.Blocks[0].(*ast.DetectBlock)), Block(prog.Blocks[0]); got != want {
		t.Errorf("Detect != Block:\n%s\n---\n%s", got, want)
	}
	if got, want := Rule(prog.Blocks[1].(*ast.RuleBlock)), Block(prog.Blocks[1]); got != want {
		t.Errorf("Rule != Block")
	}
	if got, want := Recommend(prog.Blocks[2].(*ast.RecommendBlock)), Block(prog.Blocks[2]); got != want {
		t.Errorf("Recommend != Block")
	}
	if got, want := Workflow(prog.Blocks[3].(*ast.WorkflowBlock)), Block(prog.Blocks[3]); got != want {
		t.Errorf("Workflow != Block")
	}
}
