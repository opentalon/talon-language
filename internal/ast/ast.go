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
	Recommend  *RecommendBlock
}

type RuleBlock struct {
	Pos      Pos
	Name     string
	Selector *Selector
	When     Condition
	Every    *EveryClause
	Before   *string
	After    *string
	Block    *string
	Allow    *string
	Requires *RequiresClause
	Reason   *Template
	Priority *Priority
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
	Pos      Pos
	Name     string
	Selector Selector
	Optimize OptimizeClause
	Return   []string
	Label    *Template
	Priority *Priority
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

func (*DetectBlock) blockNode()    {}
func (*RuleBlock) blockNode()      {}
func (*RecommendBlock) blockNode() {}
func (*CombineBlock) blockNode()   {}
func (*DefineBlock) blockNode()    {}
func (*WorkflowBlock) blockNode()  {}
func (*PredictBlock) blockNode()   {}
func (*ForecastBlock) blockNode()  {}
func (*ClusterBlock) blockNode()   {}
func (*ClassifyBlock) blockNode()  {}
func (*SimilarBlock) blockNode()   {}

func (b *DetectBlock) BlockName() string    { return b.Name }
func (b *RuleBlock) BlockName() string      { return b.Name }
func (b *RecommendBlock) BlockName() string { return b.Name }
func (b *CombineBlock) BlockName() string   { return b.Name }
func (b *DefineBlock) BlockName() string    { return b.Name }
func (b *WorkflowBlock) BlockName() string  { return b.Name }
func (b *PredictBlock) BlockName() string   { return b.Name }
func (b *ForecastBlock) BlockName() string  { return b.Name }
func (b *ClusterBlock) BlockName() string   { return b.Name }
func (b *ClassifyBlock) BlockName() string  { return b.Name }
func (b *SimilarBlock) BlockName() string   { return b.Name }

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

func (*AttrExpr) exprNode()          {}
func (*LiteralExpr) exprNode()       {}
func (*IdentExpr) exprNode()         {}
func (*BinaryExpr) exprNode()        {}
func (*UnaryExpr) exprNode()         {}
func (*ListExpr) exprNode()          {}
func (*ContextExpr) exprNode()       {}
func (*StepResultExpr) exprNode()    {}
func (*CategoryTreeExpr) exprNode()  {}
func (*TodayExpr) exprNode()         {}

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

// AnomalyCondition is `attr X is anomaly compared_to last N days`.
type AnomalyCondition struct {
	Subject Expr
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

// Template is a string that may contain `{ident.field}` interpolations.
type Template struct {
	Raw string
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
	Window Duration
}

type ClusterClause struct {
	ByAttrs []Expr
}

type SimilarClause struct {
	To     Expr
	Within *float64
}

type OptimizeClause struct {
	Direction string // "minimize", "maximize"
	Attr      Expr
}

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
