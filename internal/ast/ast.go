package ast

// Pos is a source location, used for error reporting.
type Pos struct {
	Line int
	Col  int
}

// Program is the top-level AST node for a .talon file.
type Program struct {
	Blocks []Block
}

// Block is implemented by all top-level block types.
type Block interface {
	blockNode()
	BlockName() string
}

// ─── Block types ──────────────────────────────────────────────────────────────

type DetectBlock struct {
	Pos        Pos
	Name       string
	Selector   Selector
	Pattern    *PatternExpr
	Flag       *FlagTarget
	Label      *Template
	Priority   *Priority
	Confidence *float64
	Calculate  []CalculateClause
	Anomaly    *AnomalyClause
	Predict    *PredictClause
	Forecast   *ForecastClause
	Cluster    *ClusterClause
	Similar    *SimilarClause
	Related    *RelatedClause
	Recommend  *RecommendBlock
	Tune       *TuneClause
}

// TuneClause names a labeled test fixture the executor uses to auto-tune
// the block's ML primitive parameters (z-threshold today; extensible to
// other primitives later) via Artificial Bee Colony optimization. See
// docs/optimizers/abc-tuning.md for the design.
type TuneClause struct {
	AgainstTest string // referenced test block name
}

type RuleBlock struct {
	Pos       Pos
	Name      string
	Selector  *Selector
	When      Condition
	Every     *EveryClause
	Before    *string
	After     *string
	Block     *string
	Allow     *string
	Requires  *RequiresClause
	Reason    *Template
	Priority  *Priority
	Strict    bool     // strict rules cannot be overridden
	Overrides []string // names of rules this rule defeats when both match
}

type RecommendBlock struct {
	Pos       Pos
	Name      string
	When      Condition
	Calculate []CalculateClause
	Suggest   *Template
	Priority  *Priority
}

type CombineBlock struct {
	Pos         Pos
	Name        string
	Selector    Selector
	Optimize    []OptimizeClause
	Constraints []ConstraintClause
	Select      *SelectClause      // nil = rank individuals (v1); non-nil = pick subset of given size (v2)
	Seed        *int64             // optional deterministic seed for GA / ACO
	Sequence    bool               // true = ACO sequence mode (permutation)
	Coordinates *CoordinatesClause // coordinate attrs for distance calc in sequence mode
	Solver      string             // "" = auto (GA/Pareto), "linear" = ILP
	Return      []string
	Label       *Template
	Priority    *Priority
}

// CoordinatesClause names the two attrs that form a 2-D position used by ACO
// to compute pairwise euclidean distance along the sequence.
type CoordinatesClause struct {
	X Expr // *AttrExpr expected
	Y Expr // *AttrExpr expected
}

type DefineBlock struct {
	Pos        Pos
	Name       string
	Params     []string
	Conditions []Condition
	ForEach    *ForEachClause
}

type WorkflowBlock struct {
	Pos   Pos
	Name  string
	Steps []WorkflowStep
}

// Top-level ML blocks — also expressible as nested clauses inside detect.

type PredictBlock struct {
	Pos        Pos
	Name       string
	Selector   Selector
	Features   []Expr
	TrainedOn  *TrainedOnClause
	Confidence *float64
	Label      *Template
	Priority   *Priority
}

type ForecastBlock struct {
	Pos      Pos
	Name     string
	Selector Selector
	Series   SeriesClause
	Predict  *ForecastPredictClause
	When     Condition
	Label    *Template
	Priority *Priority
}

type ClusterBlock struct {
	Pos      Pos
	Name     string
	Selector Selector
	ByAttrs  []Expr
	Label    *Template
	Priority *Priority
}

type ClassifyBlock struct {
	Pos        Pos
	Name       string
	Selector   Selector
	Features   []Expr
	Confidence *float64
	Label      *Template
	Priority   *Priority
}

type SimilarBlock struct {
	Pos      Pos
	Name     string
	Selector Selector
	To       Expr
	Within   *float64
	Label    *Template
	Priority *Priority
}

// RelatedBlock is `find related "name" { ... }` — Personalized PageRank
// over an entity-attribute graph projected from the FactStore. Returns
// the top-K entities most associated with one or more seeds.
//
// Exactly one of (To, Seeds) is set: To carries a single seed expression;
// Seeds carries a list literal `[e1, e2, ...]`.
type RelatedBlock struct {
	Pos      Pos
	Name     string
	Selector Selector
	To       Expr
	Seeds    []Expr
	TopK     *int
	Damping  *float64
	Tol      *float64
	MaxIter  *int
	Label    *Template
	Priority *Priority
}

// OnBlock is a reactive rule: `on change attr "x" { ... }`, `on assert <type>
// { ... }`, or `on retract <type> { ... }`. The runtime fires the block when
// the FactStore emits a matching event. See docs/reactive.md.
type OnBlock struct {
	Pos      Pos
	Name     string   // synthesized: "on change attr "x"" etc., used for diagnostics
	Trigger  string   // "change", "assert", "retract"
	Attr     string   // for change: the attribute name
	ToValue  Expr     // for change ... to <expr>: the target value (optional)
	FactType string   // for assert/retract: the fact type (e.g. "record", "activity")
	When     Condition
	Actions  []OnAction
}

// OnAction is a single statement inside an on-block body.
type OnAction interface {
	onActionNode()
}

// LoggerAction is `logger.info|warn|error "<message template>"`.
type LoggerAction struct {
	Level   string // "info", "warn", "error"
	Message Template
}

// BlockRefAction is a named reference to another top-level block, e.g.
// `recommend "Order stock"` or `detect "Defective item without ticket"`.
type BlockRefAction struct {
	Kind string // "recommend", "detect"
	Name string
}

func (*LoggerAction) onActionNode()   {}
func (*BlockRefAction) onActionNode() {}

// ConstraintBlock is an invariant checked on every fact mutation. See
// docs/constraints.md.
type ConstraintBlock struct {
	Pos         Pos
	Name        string
	Selector    Selector
	Require     Condition
	OnViolation ViolationClause
}

type ViolationClause struct {
	Mode    string // "reject" | "warn" | "quarantine"
	Message string // optional; empty if not provided
}

func (*DetectBlock) blockNode()     {}
func (*RuleBlock) blockNode()       {}
func (*RecommendBlock) blockNode()  {}
func (*CombineBlock) blockNode()    {}
func (*DefineBlock) blockNode()     {}
func (*WorkflowBlock) blockNode()   {}
func (*PredictBlock) blockNode()    {}
func (*ForecastBlock) blockNode()   {}
func (*ClusterBlock) blockNode()    {}
func (*ClassifyBlock) blockNode()   {}
func (*SimilarBlock) blockNode()    {}
func (*RelatedBlock) blockNode()    {}
func (*OnBlock) blockNode()         {}
func (*ConstraintBlock) blockNode() {}

func (b *DetectBlock) BlockName() string     { return b.Name }
func (b *RuleBlock) BlockName() string       { return b.Name }
func (b *RecommendBlock) BlockName() string  { return b.Name }
func (b *CombineBlock) BlockName() string    { return b.Name }
func (b *DefineBlock) BlockName() string     { return b.Name }
func (b *WorkflowBlock) BlockName() string   { return b.Name }
func (b *PredictBlock) BlockName() string    { return b.Name }
func (b *ForecastBlock) BlockName() string   { return b.Name }
func (b *ClusterBlock) BlockName() string    { return b.Name }
func (b *ClassifyBlock) BlockName() string   { return b.Name }
func (b *SimilarBlock) BlockName() string    { return b.Name }
func (b *RelatedBlock) BlockName() string    { return b.Name }
func (b *OnBlock) BlockName() string         { return b.Name }
func (b *ConstraintBlock) BlockName() string { return b.Name }

// ─── Expressions ──────────────────────────────────────────────────────────────

type Expr interface {
	exprNode()
}

// AttrExpr is `attr "name"`.
type AttrExpr struct {
	Name string
}

// LiteralExpr holds a string, float64, bool, or Duration.
type LiteralExpr struct {
	Value interface{}
}

// IdentExpr is a bare identifier (variable, keyword-derived name).
type IdentExpr struct {
	Name string
}

type BinaryExpr struct {
	Left  Expr
	Op    string
	Right Expr
}

type UnaryExpr struct {
	Op      string
	Operand Expr
}

type ListExpr struct {
	Elements []Expr
}

// ContextExpr is `context.field`.
type ContextExpr struct {
	Field string
}

// StepResultExpr is `step("name").result.field`.
type StepResultExpr struct {
	StepName string
	Field    string
}

// CategoryTreeExpr is `category_tree("Root")`.
type CategoryTreeExpr struct {
	Root string
}

// TodayExpr is the `today` keyword.
type TodayExpr struct{}

// MapExpr is `expr.map(field)` — extracts a field from each element of an array.
type MapExpr struct {
	Source Expr   // the array expression, e.g. step("find").result.items
	Field  string // the field to extract, e.g. "id"
}

// LearnedThresholdExpr is `learned_threshold METHOD of EXPR over last N UNIT`.
// Method is one of "p50", "p90", "p95", "p99" (or any "p<int>").
// Subject is the attr/ident expression whose historical values feed the threshold.
type LearnedThresholdExpr struct {
	Method  string
	Subject Expr
	Window  Duration
}

func (*AttrExpr) exprNode()             {}
func (*LiteralExpr) exprNode()          {}
func (*IdentExpr) exprNode()            {}
func (*BinaryExpr) exprNode()           {}
func (*UnaryExpr) exprNode()            {}
func (*ListExpr) exprNode()             {}
func (*ContextExpr) exprNode()          {}
func (*StepResultExpr) exprNode()       {}
func (*CategoryTreeExpr) exprNode()     {}
func (*TodayExpr) exprNode()            {}
func (*MapExpr) exprNode()              {}
func (*LearnedThresholdExpr) exprNode() {}

// ─── Conditions ───────────────────────────────────────────────────────────────

type Condition interface {
	condNode()
}

type CompareCondition struct {
	Left  Expr
	Op    string // "==", "!=", ">", "<", ">=", "<="
	Right Expr
}

type LogicalCondition struct {
	Op    string // "and", "or"
	Left  Condition
	Right Condition
}

type NotCondition struct {
	Inner Condition
}

type MembershipCondition struct {
	Expr    Expr
	Negated bool
	Members []Expr
}

// IsCondition is `is "define_name"` — resolves a define block by name.
type IsCondition struct {
	Subject Expr
	Name    string
}

type HasCondition struct {
	Subject Expr
	Type    string
}

type StringMatchCondition struct {
	Subject Expr
	Op      string // "contains", "starts_with", "ends_with"
	Value   string
}

// AnomalyCondition is `attr X is anomaly [using METHOD] compared_to last N days`.
// Method defaults to "zscore" when the optional `using` clause is omitted;
// the only other recognized value today is "grubbs" (Grubbs' single-outlier
// test). Validator + planner route to the matching mlruntime primitive.
type AnomalyCondition struct {
	Subject Expr
	Method  string // "zscore" (default) or "grubbs"
	Window  Duration
}

// TemporalCondition is `attr X older_than 90 days`.
type TemporalCondition struct {
	Subject Expr
	Op      string // "older_than", "newer_than"
	Value   Duration
}

// ChangedToCondition is `status changed_to "value"`.
type ChangedToCondition struct {
	Attribute string
	Value     Expr
}

// BlockMatchesCondition is `detect "name" matches` inside a recommend `when`.
type BlockMatchesCondition struct {
	Kind string // "detect", "predict", "forecast", etc.
	Name string
}

func (*CompareCondition) condNode()      {}
func (*LogicalCondition) condNode()      {}
func (*NotCondition) condNode()          {}
func (*MembershipCondition) condNode()   {}
func (*IsCondition) condNode()           {}
func (*HasCondition) condNode()          {}
func (*StringMatchCondition) condNode()  {}
func (*AnomalyCondition) condNode()      {}
func (*TemporalCondition) condNode()     {}
func (*ChangedToCondition) condNode()    {}
func (*BlockMatchesCondition) condNode() {}

// ─── Supporting types ─────────────────────────────────────────────────────────

type Selector struct {
	Target     string // "records" or a type literal
	Conditions []Condition
}

type Priority int

const (
	PriorityLow      Priority = iota
	PriorityMedium   Priority = iota
	PriorityHigh     Priority = iota
	PriorityCritical Priority = iota
)

type Duration struct {
	Value int
	Unit  string // "days", "weeks", "months", "years", "km"
}

// Template is a string that may contain `{...}` interpolations. The
// language parser pre-parses Raw into Nodes so the validator can flag
// unknown functions and the renderer doesn't reparse on every call.
//
// Nodes is empty for templates constructed without going through
// ParseTemplate (e.g. when test code synthesises one). Renderers should
// fall back to Raw + a regex pass in that case for backward compat.
type Template struct {
	Raw   string
	Nodes []TemplateNode
}

// TemplateNode is implemented by LiteralNode, RefNode, FuncNode.
type TemplateNode interface {
	templateNode()
}

// LiteralNode is plain text between interpolations.
type LiteralNode struct {
	Text string
}

// RefNode is `{path}` — a dotted reference like `item.name`, `attr.km`,
// `context.role`. Renderers resolve against the matched row.
type RefNode struct {
	Path string
}

// FuncNode is `{fn(args...)}` — a template function call. Supported
// functions: count (no args), total/sum/avg/min/max (one attr.x arg),
// days_until / days_since (one date arg). Validator enforces the
// argument count.
type FuncNode struct {
	Fn   string
	Args []string // raw arg sources — e.g. "attr.km", "expires_at"
}

func (*LiteralNode) templateNode() {}
func (*RefNode) templateNode()     {}
func (*FuncNode) templateNode()    {}

// KnownTemplateFunctions enumerates the functions ParseTemplate accepts.
// Used by the validator to surface unknown-function diagnostics.
var KnownTemplateFunctions = map[string]struct{}{
	"count":       {},
	"total":       {},
	"sum":         {},
	"avg":         {},
	"min":         {},
	"max":         {},
	"days_until":  {},
	"days_since":  {},
}

// ParseTemplate parses a raw template string into a Template with Nodes
// populated. Unknown functions are still admitted into the AST — the
// validator decides whether to reject; renderers leave them as the
// original `{...}` literal so the user sees a hint of what was wrong.
//
// Grammar:
//
//	template      = { literal | interpolation }
//	interpolation = "{" ( funcCall | path ) "}"
//	funcCall      = ident "(" [ args ] ")"
//	args          = arg { "," arg }            // whitespace-tolerant
//	path          = ident { "." ident }
func ParseTemplate(raw string) Template {
	tmpl := Template{Raw: raw}
	if raw == "" {
		return tmpl
	}
	var nodes []TemplateNode
	var lit []byte
	flushLit := func() {
		if len(lit) > 0 {
			nodes = append(nodes, &LiteralNode{Text: string(lit)})
			lit = lit[:0]
		}
	}
	i := 0
	for i < len(raw) {
		c := raw[i]
		if c != '{' {
			lit = append(lit, c)
			i++
			continue
		}
		// Find matching '}'. Templates don't nest, so the next '}' is it.
		j := i + 1
		for j < len(raw) && raw[j] != '}' {
			j++
		}
		if j >= len(raw) {
			// Unterminated — treat the rest as a literal so the bad
			// source surfaces in the rendered output.
			lit = append(lit, raw[i:]...)
			break
		}
		body := raw[i+1 : j]
		flushLit()
		nodes = append(nodes, parseInterpolation(body))
		i = j + 1
	}
	flushLit()
	tmpl.Nodes = nodes
	return tmpl
}

// parseInterpolation classifies one `{...}` body as either a function
// call or a dotted reference. Bare known-function names (e.g. `{count}`)
// are admitted as zero-arg FuncNodes so the documented `{count}` form
// works without parens.
func parseInterpolation(body string) TemplateNode {
	body = trimASCIISpace(body)
	if body == "" {
		return &LiteralNode{Text: "{}"}
	}
	if open := indexByte(body, '('); open >= 0 && lastByte(body) == ')' {
		fn := trimASCIISpace(body[:open])
		argsRaw := trimASCIISpace(body[open+1 : len(body)-1])
		return &FuncNode{Fn: fn, Args: splitArgs(argsRaw)}
	}
	if _, ok := KnownTemplateFunctions[body]; ok {
		return &FuncNode{Fn: body}
	}
	return &RefNode{Path: body}
}

func splitArgs(s string) []string {
	if s == "" {
		return nil
	}
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == ',' {
			out = append(out, trimASCIISpace(s[start:i]))
			start = i + 1
		}
	}
	out = append(out, trimASCIISpace(s[start:]))
	return out
}

func trimASCIISpace(s string) string {
	for len(s) > 0 && (s[0] == ' ' || s[0] == '\t' || s[0] == '\n') {
		s = s[1:]
	}
	for len(s) > 0 && (s[len(s)-1] == ' ' || s[len(s)-1] == '\t' || s[len(s)-1] == '\n') {
		s = s[:len(s)-1]
	}
	return s
}

func indexByte(s string, b byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return i
		}
	}
	return -1
}

func lastByte(s string) byte {
	if s == "" {
		return 0
	}
	return s[len(s)-1]
}

type FlagTarget struct {
	Kind string // "items", "records", or a custom type name
}

// PatternExpr covers multi-record patterns: "when 3+ records same category".
type PatternExpr struct {
	MinCount int
	GroupBy  string
	Window   *Duration
}

type EveryClause struct {
	Value  int
	Unit   string // "km", "days", etc.
	OnAttr string
}

type RequiresClause struct {
	What     string
	Approval *ApprovalExpr
}

type ApprovalExpr struct {
	Role string
}

type CalculateClause struct {
	Name   string
	From   string // "activities", "records"
	Where  []Condition
	Within *Duration
}

// PredictClause is the nested ML predict inside a detect block.
type PredictClause struct {
	Features   []Expr
	TrainedOn  *TrainedOnClause
	Confidence *float64
}

type TrainedOnClause struct {
	Conditions []Condition
}

// ForecastClause is the nested ML forecast inside a detect block.
type ForecastClause struct {
	Series  SeriesClause
	Predict *ForecastPredictClause
}

type SeriesClause struct {
	Attr   Expr
	Window Duration
}

// ForecastPredictClause is `predict days_until value <= 0` inside a forecast.
type ForecastPredictClause struct {
	Variable  string
	Condition Condition
}

type AnomalyClause struct {
	Method string // "zscore" (default) or "grubbs"
	Window Duration
}

type ClusterClause struct {
	ByAttrs []Expr
}

type SimilarClause struct {
	To     Expr
	Within *float64
}

// RelatedClause is the nested form of `find related ...` inside a detect block.
type RelatedClause struct {
	To      Expr
	Seeds   []Expr
	TopK    *int
	Damping *float64
}

type OptimizeClause struct {
	Direction string // "minimize", "maximize"
	Attr      Expr   // may be *AggregateExpr in v2 subset mode
}

// SelectClause is `select K from records` inside a combine block.
// Marks the block as a subset-selection problem rather than per-row Pareto.
type SelectClause struct {
	Size int
}

// ConstraintClause is `subject_to AGG OP LITERAL`. v2 supports a single
// aggregate-vs-literal inequality form; richer expressions can land later
// without breaking this shape (Expr is general).
type ConstraintClause struct {
	Pos   Pos
	Left  Expr   // typically *AggregateExpr over selected subset
	Op    string // "<=", ">=", "<", ">", "==", "!="
	Right Expr   // typically *LiteralExpr (numeric)
}

// AggregateExpr is `total(...)`, `count(...)`, `avg(...)` evaluated over
// the selected subset's rows.
type AggregateExpr struct {
	Fn  string // "total", "count", "avg"
	Arg Expr   // typically *AttrExpr; for `count(records)` Arg may be nil
}

func (*AggregateExpr) exprNode() {}

type ForEachClause struct {
	Variable string
	Over     Expr
	Body     []Condition
}

type WorkflowStep struct {
	Name      string
	DependsOn []string
	MCPCall   *MCPCall
}

type MCPCall struct {
	Server string
	Tool   string
	Args   map[string]Expr
}

// ─── Test types ──────────────────────────────────────────────────────────────

// TestBlock is a single test case in a .talon.test file.
type TestBlock struct {
	Pos       Pos
	Name      string
	Given     []TestDatum
	WhenKind  string // "detect", "rule", "forecast", etc.
	WhenBlock string // block name
	Expect    []TestAssertion
}

func (*TestBlock) blockNode()          {}
func (b *TestBlock) BlockName() string { return b.Name }

// TestDatum is one line inside a given { } block.
type TestDatum struct {
	Kind   string                 // "record" or "attr"
	ID     int                    // record/entity ID
	Fields map[string]interface{} // key → value pairs
}

// TestAssertion is one line inside an expect { } block.
type TestAssertion struct {
	Kind  string // "flagged", "not_flagged", "label", "priority", "count"
	ID    int    // entity ID (for flagged/not_flagged)
	Op    string // "contains", "==", etc.
	Value string // expected value
}
