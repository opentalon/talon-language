package parser

import (
	"fmt"
	"strconv"

	"github.com/opentalon/talon-language/internal/ast"
	"github.com/opentalon/talon-language/internal/diagnostic"
	"github.com/opentalon/talon-language/internal/lexer"
)

type parser struct {
	file   string
	tokens []lexer.Token
	pos    int
	diags  diagnostic.List
}

func Parse(file string, tokens []lexer.Token) (*ast.Program, diagnostic.List) {
	p := &parser{file: file, tokens: tokens}
	return p.parseProgram(), p.diags
}

func (p *parser) parseProgram() *ast.Program {
	prog := &ast.Program{}
	for !p.at(lexer.TokenEOF) {
		b := p.parseBlock()
		if b != nil {
			prog.Blocks = append(prog.Blocks, b)
		}
	}
	return prog
}

// ─── Block dispatch ───────────────────────────────────────────────────────────

func (p *parser) parseBlock() ast.Block {
	switch p.peek().Type {
	case lexer.TokenDetect:
		return p.parseDetect()
	case lexer.TokenRule:
		return p.parseRule()
	case lexer.TokenRecommend:
		return p.parseRecommend()
	case lexer.TokenCombine:
		return p.parseCombine()
	case lexer.TokenDefine:
		return p.parseDefine()
	case lexer.TokenWorkflow:
		return p.parseWorkflow()
	case lexer.TokenPredict:
		return p.parsePredictBlock()
	case lexer.TokenForecast:
		return p.parseForecastBlock()
	case lexer.TokenCluster:
		return p.parseClusterBlock()
	case lexer.TokenClassify:
		return p.parseClassifyBlock()
	case lexer.TokenFind:
		return p.parseSimilarBlock()
	case lexer.TokenTest:
		return p.parseTestBlock()
	default:
		p.errorf("expected block keyword (detect, rule, recommend, ...), got %q", p.peek().Value)
		p.synchronize()
		return nil
	}
}

// ─── detect ───────────────────────────────────────────────────────────────────

func (p *parser) parseDetect() *ast.DetectBlock {
	tok := p.advance() // detect
	name := p.expectString()
	if !p.expect(lexer.TokenLBrace) {
		p.synchronize()
		return nil
	}
	b := &ast.DetectBlock{Name: name, Pos: ast.Pos{Line: tok.Line, Col: tok.Col}}
	if p.at(lexer.TokenFor) {
		b.Selector = p.parseSelector()
	}
	for !p.at(lexer.TokenRBrace) && !p.at(lexer.TokenEOF) {
		if !p.parseDetectClause(b) {
			p.synchronizeInBlock()
		}
	}
	p.expect(lexer.TokenRBrace)
	return b
}

func (p *parser) parseDetectClause(b *ast.DetectBlock) bool {
	switch p.peek().Type {
	case lexer.TokenFlag:
		b.Flag = p.parseFlagTarget()
	case lexer.TokenLabel:
		b.Label = p.parseLabelClause()
	case lexer.TokenPriority:
		pr := p.parsePriority()
		b.Priority = &pr
	case lexer.TokenConfidence:
		c := p.parseConfidenceClause()
		b.Confidence = &c
	case lexer.TokenCalculate:
		b.Calculate = append(b.Calculate, p.parseCalculateClause())
	case lexer.TokenIs:
		// "is anomaly compared_to last N UNIT" — standalone anomaly clause
		b.Anomaly = p.parseAnomalyClause()
	case lexer.TokenPredict:
		b.Predict = p.parseNestedPredictClause()
	case lexer.TokenForecast:
		b.Forecast = p.parseNestedForecastClause()
	case lexer.TokenCluster:
		b.Cluster = p.parseNestedClusterClause()
	case lexer.TokenFind:
		b.Similar = p.parseNestedSimilarClause()
	case lexer.TokenRecommend:
		rec := p.parseRecommend()
		b.Recommend = rec
	case lexer.TokenWhen:
		b.Pattern = p.parsePatternExpr()
	case lexer.TokenTune:
		b.Tune = p.parseTuneClause()
	default:
		p.errorf("unexpected token %q inside detect block", p.peek().Value)
		return false
	}
	return true
}

// parseTuneClause parses `tune against test "NAME"`. The named test must be
// defined in a .talon.test file passed to `talon test` / `talon explain`;
// its given/expect data becomes the labeled fixture ABC tunes against.
func (p *parser) parseTuneClause() *ast.TuneClause {
	p.advance() // tune
	if !p.at(lexer.TokenAgainst) {
		p.errorf("expected 'against' after 'tune', got %q", p.peek().Value)
		return nil
	}
	p.advance()
	if !p.at(lexer.TokenTest) {
		p.errorf("expected 'test' after 'tune against', got %q", p.peek().Value)
		return nil
	}
	p.advance()
	name := p.expectString()
	return &ast.TuneClause{AgainstTest: name}
}

// ─── rule ─────────────────────────────────────────────────────────────────────

func (p *parser) parseRule() *ast.RuleBlock {
	tok := p.advance() // rule
	name := p.expectString()
	if !p.expect(lexer.TokenLBrace) {
		p.synchronize()
		return nil
	}
	b := &ast.RuleBlock{Name: name, Pos: ast.Pos{Line: tok.Line, Col: tok.Col}}
	// selector or when
	if p.at(lexer.TokenFor) {
		sel := p.parseSelector()
		b.Selector = &sel
	} else if p.at(lexer.TokenWhen) {
		b.When = p.parseWhenClause()
	}
	for !p.at(lexer.TokenRBrace) && !p.at(lexer.TokenEOF) {
		if !p.parseRuleClause(b) {
			p.synchronizeInBlock()
		}
	}
	p.expect(lexer.TokenRBrace)
	return b
}

func (p *parser) parseRuleClause(b *ast.RuleBlock) bool {
	switch p.peek().Type {
	case lexer.TokenEvery:
		b.Every = p.parseEveryClause()
	case lexer.TokenBefore:
		p.advance()
		s := p.expectString()
		b.Before = &s
	case lexer.TokenAfter:
		p.advance()
		s := p.expectString()
		b.After = &s
	case lexer.TokenBlock:
		p.advance() // block
		if p.at(lexer.TokenString) {
			s := p.advance().Value
			b.Block = &s
		}
		// optional inline "reason"
		if p.at(lexer.TokenReason) {
			b.Reason = p.parseLabelClause()
		}
	case lexer.TokenAllow:
		p.advance()
		s := p.expectString()
		b.Allow = &s
	case lexer.TokenRequires:
		b.Requires = p.parseRequiresClause()
	case lexer.TokenReason:
		b.Reason = p.parseLabelClause()
	case lexer.TokenPriority:
		pr := p.parsePriority()
		b.Priority = &pr
	default:
		p.errorf("unexpected token %q inside rule block", p.peek().Value)
		return false
	}
	return true
}

// ─── recommend ────────────────────────────────────────────────────────────────

func (p *parser) parseRecommend() *ast.RecommendBlock {
	tok := p.advance() // recommend
	name := p.expectString()
	if !p.expect(lexer.TokenLBrace) {
		p.synchronize()
		return nil
	}
	b := &ast.RecommendBlock{Name: name, Pos: ast.Pos{Line: tok.Line, Col: tok.Col}}
	if p.at(lexer.TokenWhen) {
		b.When = p.parseWhenClause()
	}
	for !p.at(lexer.TokenRBrace) && !p.at(lexer.TokenEOF) {
		switch p.peek().Type {
		case lexer.TokenCalculate:
			b.Calculate = append(b.Calculate, p.parseCalculateClause())
		case lexer.TokenSuggest:
			b.Suggest = p.parseLabelClause()
		case lexer.TokenPriority:
			pr := p.parsePriority()
			b.Priority = &pr
		default:
			p.errorf("unexpected token %q inside recommend block", p.peek().Value)
			p.synchronizeInBlock()
		}
	}
	p.expect(lexer.TokenRBrace)
	return b
}

// ─── combine ──────────────────────────────────────────────────────────────────

func (p *parser) parseCombine() *ast.CombineBlock {
	tok := p.advance() // combine
	name := p.expectString()
	if !p.expect(lexer.TokenLBrace) {
		p.synchronize()
		return nil
	}
	b := &ast.CombineBlock{Name: name, Pos: ast.Pos{Line: tok.Line, Col: tok.Col}}
	if p.at(lexer.TokenFor) {
		b.Selector = p.parseSelector()
	}
	for !p.at(lexer.TokenRBrace) && !p.at(lexer.TokenEOF) {
		switch p.peek().Type {
		case lexer.TokenMinimize, lexer.TokenMaximize:
			dir := p.advance().Value
			attr := p.parseExpr()
			b.Optimize = append(b.Optimize, ast.OptimizeClause{Direction: dir, Attr: attr})
		case lexer.TokenSelect:
			p.advance()
			n, _ := strconv.Atoi(p.expectNumberStr())
			// optional `from records`
			if p.at(lexer.TokenFrom) {
				p.advance()
				if p.at(lexer.TokenRecords) {
					p.advance()
				}
			}
			b.Select = &ast.SelectClause{Size: n}
		case lexer.TokenSubjectTo:
			tok := p.advance()
			left := p.parseExpr()
			op := p.parseCompareOp()
			right := p.parseExpr()
			b.Constraints = append(b.Constraints, ast.ConstraintClause{
				Pos:   ast.Pos{Line: tok.Line, Col: tok.Col},
				Left:  left,
				Op:    op,
				Right: right,
			})
		case lexer.TokenSeed:
			p.advance()
			n, _ := strconv.ParseInt(p.expectNumberStr(), 10, 64)
			b.Seed = &n
		case lexer.TokenSequence:
			p.advance()
			b.Sequence = true
		case lexer.TokenCoordinates:
			p.advance()
			x := p.parseExpr()
			p.expect(lexer.TokenComma)
			y := p.parseExpr()
			b.Coordinates = &ast.CoordinatesClause{X: x, Y: y}
		case lexer.TokenSolver:
			p.advance()
			if p.at(lexer.TokenLinear) {
				p.advance()
				b.Solver = "linear"
			} else {
				p.errorf("expected solver type (linear), got %q", p.peek().Value)
			}
		case lexer.TokenReturn:
			p.advance()
			b.Return = append(b.Return, p.expectIdent())
			for p.at(lexer.TokenComma) {
				p.advance()
				b.Return = append(b.Return, p.expectIdent())
			}
		case lexer.TokenLabel:
			b.Label = p.parseLabelClause()
		case lexer.TokenPriority:
			pr := p.parsePriority()
			b.Priority = &pr
		default:
			p.errorf("unexpected token %q inside combine block", p.peek().Value)
			p.synchronizeInBlock()
		}
	}
	p.expect(lexer.TokenRBrace)
	return b
}

// parseCompareOp consumes one of ==, !=, <, <=, >, >= and returns the source
// form. Used by subject_to clauses where the LHS is an aggregate expression
// and the RHS is typically a numeric literal.
func (p *parser) parseCompareOp() string {
	switch p.peek().Type {
	case lexer.TokenEq:
		p.advance()
		return "=="
	case lexer.TokenNeq:
		p.advance()
		return "!="
	case lexer.TokenLt:
		p.advance()
		return "<"
	case lexer.TokenLte:
		p.advance()
		return "<="
	case lexer.TokenGt:
		p.advance()
		return ">"
	case lexer.TokenGte:
		p.advance()
		return ">="
	}
	p.errorf("expected comparison operator, got %q", p.peek().Value)
	return "=="
}

// ─── define ───────────────────────────────────────────────────────────────────

func (p *parser) parseDefine() *ast.DefineBlock {
	tok := p.advance() // define
	name := p.expectString()
	var params []string
	if p.at(lexer.TokenLParen) {
		p.advance()
		for !p.at(lexer.TokenRParen) && !p.at(lexer.TokenEOF) {
			params = append(params, p.expectIdent())
			if p.at(lexer.TokenComma) {
				p.advance()
			}
		}
		p.expect(lexer.TokenRParen)
	}
	if !p.expect(lexer.TokenLBrace) {
		p.synchronize()
		return nil
	}
	b := &ast.DefineBlock{Name: name, Params: params, Pos: ast.Pos{Line: tok.Line, Col: tok.Col}}
	for !p.at(lexer.TokenRBrace) && !p.at(lexer.TokenEOF) {
		if p.at(lexer.TokenFor) && p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Type == lexer.TokenEach {
			b.ForEach = p.parseForEachClause()
		} else {
			cond := p.parseOrCondition()
			if cond != nil {
				b.Conditions = append(b.Conditions, cond)
			}
		}
	}
	p.expect(lexer.TokenRBrace)
	return b
}

// ─── workflow ─────────────────────────────────────────────────────────────────

func (p *parser) parseWorkflow() *ast.WorkflowBlock {
	tok := p.advance() // workflow
	name := p.expectString()
	if !p.expect(lexer.TokenLBrace) {
		p.synchronize()
		return nil
	}
	b := &ast.WorkflowBlock{Name: name, Pos: ast.Pos{Line: tok.Line, Col: tok.Col}}
	for !p.at(lexer.TokenRBrace) && !p.at(lexer.TokenEOF) {
		if p.at(lexer.TokenStep) {
			b.Steps = append(b.Steps, p.parseWorkflowStep())
		} else {
			p.errorf("expected step inside workflow, got %q", p.peek().Value)
			p.synchronizeInBlock()
		}
	}
	p.expect(lexer.TokenRBrace)
	return b
}

func (p *parser) parseWorkflowStep() ast.WorkflowStep {
	p.advance() // step
	name := p.expectString()
	var dependsOn []string
	if p.at(lexer.TokenDepends) {
		p.advance() // depends_on
		if p.at(lexer.TokenLBracket) {
			p.advance()
			for !p.at(lexer.TokenRBracket) && !p.at(lexer.TokenEOF) {
				dependsOn = append(dependsOn, p.expectString())
				if p.at(lexer.TokenComma) {
					p.advance()
				}
			}
			p.expect(lexer.TokenRBracket)
		} else {
			dependsOn = append(dependsOn, p.expectString())
		}
	}
	p.expect(lexer.TokenLBrace)
	step := ast.WorkflowStep{Name: name, DependsOn: dependsOn}
	if p.at(lexer.TokenMcp) {
		step.MCPCall = p.parseMCPCall()
	}
	p.expect(lexer.TokenRBrace)
	return step
}

func (p *parser) parseMCPCall() *ast.MCPCall {
	p.advance() // mcp
	server := p.expectString()
	tool := p.expectString()
	p.expect(lexer.TokenLBrace)
	call := &ast.MCPCall{Server: server, Tool: tool, Args: map[string]ast.Expr{}}
	for !p.at(lexer.TokenRBrace) && !p.at(lexer.TokenEOF) {
		key := p.expectIdent()
		val := p.parseExpr()
		call.Args[key] = val
	}
	p.expect(lexer.TokenRBrace)
	return call
}

// ─── top-level ML blocks ──────────────────────────────────────────────────────

func (p *parser) parsePredictBlock() *ast.PredictBlock {
	tok := p.advance() // predict
	name := p.expectString()
	if !p.expect(lexer.TokenLBrace) {
		p.synchronize()
		return nil
	}
	b := &ast.PredictBlock{Name: name, Pos: ast.Pos{Line: tok.Line, Col: tok.Col}}
	if p.at(lexer.TokenFor) {
		b.Selector = p.parseSelector()
	}
	for !p.at(lexer.TokenRBrace) && !p.at(lexer.TokenEOF) {
		switch p.peek().Type {
		case lexer.TokenFeatures:
			b.Features = p.parseFeaturesClause()
		case lexer.TokenTrainedOn:
			b.TrainedOn = p.parseTrainedOnClause()
		case lexer.TokenConfidence:
			c := p.parseConfidenceClause()
			b.Confidence = &c
		case lexer.TokenLabel:
			b.Label = p.parseLabelClause()
		case lexer.TokenPriority:
			pr := p.parsePriority()
			b.Priority = &pr
		default:
			p.errorf("unexpected token %q inside predict block", p.peek().Value)
			p.synchronizeInBlock()
		}
	}
	p.expect(lexer.TokenRBrace)
	return b
}

func (p *parser) parseForecastBlock() *ast.ForecastBlock {
	tok := p.advance() // forecast
	name := p.expectString()
	if !p.expect(lexer.TokenLBrace) {
		p.synchronize()
		return nil
	}
	b := &ast.ForecastBlock{Name: name, Pos: ast.Pos{Line: tok.Line, Col: tok.Col}}
	if p.at(lexer.TokenFor) {
		b.Selector = p.parseSelector()
	}
	for !p.at(lexer.TokenRBrace) && !p.at(lexer.TokenEOF) {
		switch p.peek().Type {
		case lexer.TokenSeries:
			b.Series = p.parseSeriesClause()
		case lexer.TokenPredict:
			b.Predict = p.parseForecastPredictClause()
		case lexer.TokenWhen:
			b.When = p.parseWhenClause()
		case lexer.TokenLabel:
			b.Label = p.parseLabelClause()
		case lexer.TokenPriority:
			pr := p.parsePriority()
			b.Priority = &pr
		default:
			p.errorf("unexpected token %q inside forecast block", p.peek().Value)
			p.synchronizeInBlock()
		}
	}
	p.expect(lexer.TokenRBrace)
	return b
}

func (p *parser) parseClusterBlock() *ast.ClusterBlock {
	tok := p.advance() // cluster
	name := p.expectString()
	if !p.expect(lexer.TokenLBrace) {
		p.synchronize()
		return nil
	}
	b := &ast.ClusterBlock{Name: name, Pos: ast.Pos{Line: tok.Line, Col: tok.Col}}
	if p.at(lexer.TokenFor) {
		b.Selector = p.parseSelector()
	}
	for !p.at(lexer.TokenRBrace) && !p.at(lexer.TokenEOF) {
		switch p.peek().Type {
		case lexer.TokenIdent:
			if p.peek().Value == "by" {
				p.advance() // by
				b.ByAttrs = p.parseAttrList()
			} else {
				p.errorf("unexpected %q inside cluster block", p.peek().Value)
				p.synchronizeInBlock()
			}
		case lexer.TokenLabel:
			b.Label = p.parseLabelClause()
		case lexer.TokenPriority:
			pr := p.parsePriority()
			b.Priority = &pr
		default:
			p.errorf("unexpected token %q inside cluster block", p.peek().Value)
			p.synchronizeInBlock()
		}
	}
	p.expect(lexer.TokenRBrace)
	return b
}

func (p *parser) parseClassifyBlock() *ast.ClassifyBlock {
	tok := p.advance() // classify
	name := p.expectString()
	if !p.expect(lexer.TokenLBrace) {
		p.synchronize()
		return nil
	}
	b := &ast.ClassifyBlock{Name: name, Pos: ast.Pos{Line: tok.Line, Col: tok.Col}}
	if p.at(lexer.TokenFor) {
		b.Selector = p.parseSelector()
	}
	for !p.at(lexer.TokenRBrace) && !p.at(lexer.TokenEOF) {
		switch p.peek().Type {
		case lexer.TokenFeatures:
			b.Features = p.parseFeaturesClause()
		case lexer.TokenConfidence:
			c := p.parseConfidenceClause()
			b.Confidence = &c
		case lexer.TokenLabel:
			b.Label = p.parseLabelClause()
		case lexer.TokenPriority:
			pr := p.parsePriority()
			b.Priority = &pr
		default:
			p.errorf("unexpected token %q inside classify block", p.peek().Value)
			p.synchronizeInBlock()
		}
	}
	p.expect(lexer.TokenRBrace)
	return b
}

func (p *parser) parseSimilarBlock() *ast.SimilarBlock {
	tok := p.advance() // find
	// expect "similar"
	if p.peek().Type == lexer.TokenSimilar {
		p.advance()
	} else {
		p.errorf("expected 'similar' after 'find', got %q", p.peek().Value)
	}
	name := p.expectString()
	if !p.expect(lexer.TokenLBrace) {
		p.synchronize()
		return nil
	}
	b := &ast.SimilarBlock{Name: name, Pos: ast.Pos{Line: tok.Line, Col: tok.Col}}
	if p.at(lexer.TokenFor) {
		b.Selector = p.parseSelector()
	}
	for !p.at(lexer.TokenRBrace) && !p.at(lexer.TokenEOF) {
		switch p.peek().Type {
		case lexer.TokenIdent:
			if p.peek().Value == "to" {
				p.advance()
				b.To = p.parseExpr()
			} else if p.peek().Value == "within" {
				p.advance()
				n, _ := strconv.ParseFloat(p.expectNumberStr(), 64)
				b.Within = &n
			} else {
				p.errorf("unexpected %q inside find similar block", p.peek().Value)
				p.synchronizeInBlock()
			}
		case lexer.TokenWithin:
			p.advance()
			n, _ := strconv.ParseFloat(p.expectNumberStr(), 64)
			b.Within = &n
		case lexer.TokenLabel:
			b.Label = p.parseLabelClause()
		case lexer.TokenPriority:
			pr := p.parsePriority()
			b.Priority = &pr
		default:
			p.errorf("unexpected token %q inside find similar block", p.peek().Value)
			p.synchronizeInBlock()
		}
	}
	p.expect(lexer.TokenRBrace)
	return b
}

// ─── Clauses ──────────────────────────────────────────────────────────────────

func (p *parser) parseSelector() ast.Selector {
	p.advance() // for
	// "records" keyword
	if p.at(lexer.TokenRecords) {
		p.advance()
	} else {
		p.errorf("expected 'records' after 'for', got %q", p.peek().Value)
	}
	p.expect(lexer.TokenWhere)
	cond := p.parseOrCondition()
	return ast.Selector{Target: "records", Conditions: []ast.Condition{cond}}
}

func (p *parser) parseFlagTarget() *ast.FlagTarget {
	p.advance() // flag
	if p.at(lexer.TokenMatching) {
		p.advance()
	}
	kind := "items"
	switch p.peek().Type {
	case lexer.TokenItems:
		kind = p.advance().Value
	case lexer.TokenRecords:
		kind = p.advance().Value
	case lexer.TokenIdent:
		kind = p.advance().Value
	}
	return &ast.FlagTarget{Kind: kind}
}

func (p *parser) parseLabelClause() *ast.Template {
	p.advance() // label / suggest / reason
	raw := p.expectString()
	return &ast.Template{Raw: raw}
}

func (p *parser) parsePriority() ast.Priority {
	p.advance() // priority
	switch p.peek().Type {
	case lexer.TokenLow:
		p.advance()
		return ast.PriorityLow
	case lexer.TokenMedium:
		p.advance()
		return ast.PriorityMedium
	case lexer.TokenHigh:
		p.advance()
		return ast.PriorityHigh
	case lexer.TokenCritical:
		p.advance()
		return ast.PriorityCritical
	default:
		p.errorf("expected LOW/MEDIUM/HIGH/CRITICAL, got %q", p.peek().Value)
		return ast.PriorityLow
	}
}

func (p *parser) parseConfidenceClause() float64 {
	p.advance() // confidence
	p.expect(lexer.TokenGte)
	n, _ := strconv.ParseFloat(p.expectNumberStr(), 64)
	return n
}

func (p *parser) parseCalculateClause() ast.CalculateClause {
	p.advance() // calculate
	name := p.expectIdent()
	p.expect(lexer.TokenFrom)
	from := p.expectIdent()
	calc := ast.CalculateClause{Name: name, From: from}
	if p.at(lexer.TokenWhere) {
		p.advance()
		cond := p.parseOrCondition()
		calc.Where = []ast.Condition{cond}
	}
	if p.at(lexer.TokenWithin) {
		p.advance()
		p.expect(lexer.TokenLast)
		n, _ := strconv.Atoi(p.expectNumberStr())
		unit := p.expectDurationUnit()
		calc.Within = &ast.Duration{Value: n, Unit: unit}
	}
	return calc
}

func (p *parser) parseEveryClause() *ast.EveryClause {
	p.advance() // every
	n, _ := strconv.Atoi(p.expectNumberStr())
	unit := p.peekDurationUnitOrIdent()
	every := &ast.EveryClause{Value: n, Unit: unit}
	p.expect(lexer.TokenOn)
	p.expect(lexer.TokenAttr)
	every.OnAttr = p.expectString()
	return every
}

func (p *parser) parseRequiresClause() *ast.RequiresClause {
	p.advance() // requires
	if p.at(lexer.TokenApproval) {
		p.advance() // approval
		p.expect(lexer.TokenFrom)
		p.expect(lexer.TokenRole)
		role := p.expectString()
		return &ast.RequiresClause{Approval: &ast.ApprovalExpr{Role: role}}
	}
	what := p.expectString()
	return &ast.RequiresClause{What: what}
}

func (p *parser) parseFeaturesClause() []ast.Expr {
	p.advance() // features
	p.expect(lexer.TokenLBracket)
	var features []ast.Expr
	for !p.at(lexer.TokenRBracket) && !p.at(lexer.TokenEOF) {
		features = append(features, p.parseExpr())
		if p.at(lexer.TokenComma) {
			p.advance()
		}
	}
	p.expect(lexer.TokenRBracket)
	return features
}

func (p *parser) parseTrainedOnClause() *ast.TrainedOnClause {
	p.advance() // trained_on
	p.expect(lexer.TokenRecords)
	p.expect(lexer.TokenWhere)
	cond := p.parseOrCondition()
	return &ast.TrainedOnClause{Conditions: []ast.Condition{cond}}
}

func (p *parser) parseSeriesClause() ast.SeriesClause {
	p.advance() // series
	attr := p.parseExpr()
	p.expect(lexer.TokenOver)
	p.expect(lexer.TokenLast)
	n, _ := strconv.Atoi(p.expectNumberStr())
	unit := p.expectDurationUnit()
	return ast.SeriesClause{Attr: attr, Window: ast.Duration{Value: n, Unit: unit}}
}

func (p *parser) parseForecastPredictClause() *ast.ForecastPredictClause {
	p.advance() // predict
	variable := p.expectIdent()
	// optional "value" keyword
	if p.at(lexer.TokenIdent) && p.peek().Value == "value" {
		p.advance()
	}
	cond := p.parseOrCondition()
	return &ast.ForecastPredictClause{Variable: variable, Condition: cond}
}

func (p *parser) parseAnomalyClause() *ast.AnomalyClause {
	p.advance() // is
	p.expect(lexer.TokenAnomaly)
	p.expect(lexer.TokenComparedTo)
	p.expect(lexer.TokenLast)
	n, _ := strconv.Atoi(p.expectNumberStr())
	unit := p.expectDurationUnit()
	return &ast.AnomalyClause{Window: ast.Duration{Value: n, Unit: unit}}
}

func (p *parser) parseNestedPredictClause() *ast.PredictClause {
	p.advance() // predict
	clause := &ast.PredictClause{}
	if p.at(lexer.TokenFeatures) {
		clause.Features = p.parseFeaturesClause()
	}
	if p.at(lexer.TokenTrainedOn) {
		clause.TrainedOn = p.parseTrainedOnClause()
	}
	if p.at(lexer.TokenConfidence) {
		c := p.parseConfidenceClause()
		clause.Confidence = &c
	}
	return clause
}

func (p *parser) parseNestedForecastClause() *ast.ForecastClause {
	p.advance() // forecast
	clause := &ast.ForecastClause{}
	if p.at(lexer.TokenSeries) {
		clause.Series = p.parseSeriesClause()
	}
	if p.at(lexer.TokenPredict) {
		clause.Predict = p.parseForecastPredictClause()
	}
	return clause
}

func (p *parser) parseNestedClusterClause() *ast.ClusterClause {
	p.advance() // cluster
	// "by" is an ident
	if p.at(lexer.TokenIdent) && p.peek().Value == "by" {
		p.advance()
	}
	return &ast.ClusterClause{ByAttrs: p.parseAttrList()}
}

func (p *parser) parseNestedSimilarClause() *ast.SimilarClause {
	p.advance() // find
	if p.at(lexer.TokenSimilar) {
		p.advance()
	}
	// "to"
	if p.at(lexer.TokenIdent) && p.peek().Value == "to" {
		p.advance()
	}
	clause := &ast.SimilarClause{To: p.parseExpr()}
	if p.at(lexer.TokenWithin) {
		p.advance()
		n, _ := strconv.ParseFloat(p.expectNumberStr(), 64)
		clause.Within = &n
	}
	return clause
}

func (p *parser) parsePatternExpr() *ast.PatternExpr {
	p.advance() // when
	// "N+ records same ATTR [within N UNIT]"
	n, _ := strconv.Atoi(p.expectNumberStr())
	if p.at(lexer.TokenPlus) {
		p.advance()
	}
	if p.at(lexer.TokenRecords) {
		p.advance()
	}
	var groupBy string
	if p.at(lexer.TokenSame) {
		p.advance()
		groupBy = p.expectIdent()
	}
	pattern := &ast.PatternExpr{MinCount: n, GroupBy: groupBy}
	if p.at(lexer.TokenWithin) {
		p.advance()
		n2, _ := strconv.Atoi(p.expectNumberStr())
		unit := p.expectDurationUnit()
		dur := ast.Duration{Value: n2, Unit: unit}
		pattern.Window = &dur
	}
	return pattern
}

func (p *parser) parseAttrList() []ast.Expr {
	var list []ast.Expr
	list = append(list, p.parseExpr())
	for p.at(lexer.TokenComma) {
		p.advance()
		list = append(list, p.parseExpr())
	}
	return list
}

func (p *parser) parseForEachClause() *ast.ForEachClause {
	p.advance() // for
	p.advance() // each
	variable := p.expectIdent()
	p.expect(lexer.TokenIn)
	over := p.parseExpr()
	p.expect(lexer.TokenLBrace)
	clause := &ast.ForEachClause{Variable: variable, Over: over}
	for !p.at(lexer.TokenRBrace) && !p.at(lexer.TokenEOF) {
		cond := p.parseOrCondition()
		if cond != nil {
			clause.Body = append(clause.Body, cond)
		}
	}
	p.expect(lexer.TokenRBrace)
	return clause
}

// ─── When clause ──────────────────────────────────────────────────────────────

func (p *parser) parseWhenClause() ast.Condition {
	p.advance() // when
	// "detect/predict/forecast/recommend NAME matches"
	if p.isBlockKeyword(p.peek().Type) {
		kind := p.advance().Value
		name := p.expectString()
		if p.at(lexer.TokenIdent) && p.peek().Value == "matches" {
			p.advance()
		}
		return &ast.BlockMatchesCondition{Kind: kind, Name: name}
	}
	return p.parseOrCondition()
}

func (p *parser) isBlockKeyword(tt lexer.TokenType) bool {
	switch tt {
	case lexer.TokenDetect, lexer.TokenPredict, lexer.TokenForecast,
		lexer.TokenCluster, lexer.TokenClassify, lexer.TokenFind:
		return true
	}
	return false
}

// ─── Condition parsing ────────────────────────────────────────────────────────

func (p *parser) parseOrCondition() ast.Condition {
	left := p.parseAndCondition()
	for p.at(lexer.TokenOr) {
		p.advance()
		right := p.parseAndCondition()
		left = &ast.LogicalCondition{Op: "or", Left: left, Right: right}
	}
	return left
}

func (p *parser) parseAndCondition() ast.Condition {
	left := p.parseNotCondition()
	for p.at(lexer.TokenAnd) {
		p.advance()
		right := p.parseNotCondition()
		left = &ast.LogicalCondition{Op: "and", Left: left, Right: right}
	}
	return left
}

func (p *parser) parseNotCondition() ast.Condition {
	if p.at(lexer.TokenNot) {
		// could be "not X" or "not in" — "not in" is handled in parseAtomCondition
		// peek ahead: if next is "in", let parseAtomCondition handle via an Expr + "not in"
		// For standalone "not COND", consume not and recurse
		if p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Type != lexer.TokenIn {
			p.advance()
			return &ast.NotCondition{Inner: p.parseAtomCondition()}
		}
	}
	return p.parseAtomCondition()
}

func (p *parser) parseAtomCondition() ast.Condition {
	switch p.peek().Type {
	case lexer.TokenHas:
		return p.parseHasCondition()
	case lexer.TokenIs:
		// standalone "is STRING" — no subject
		p.advance()
		if p.at(lexer.TokenAnomaly) {
			// "is anomaly compared_to last N UNIT" inside a condition context
			p.advance() // anomaly
			p.expect(lexer.TokenComparedTo)
			p.expect(lexer.TokenLast)
			n, _ := strconv.Atoi(p.expectNumberStr())
			unit := p.expectDurationUnit()
			return &ast.AnomalyCondition{Window: ast.Duration{Value: n, Unit: unit}}
		}
		name := p.expectString()
		return &ast.IsCondition{Name: name}
	default:
		return p.parseExprCondition()
	}
}

func (p *parser) parseHasCondition() ast.Condition {
	p.advance() // has
	// optional "open", "record", etc. — consume idents until "type"
	for (p.at(lexer.TokenIdent) || p.at(lexer.TokenRecords)) && !p.atEOF() {
		if p.peek().Value == "type" {
			break
		}
		p.advance()
	}
	p.advance() // type
	name := p.expectString()
	return &ast.HasCondition{Type: name}
}

func (p *parser) parseExprCondition() ast.Condition {
	expr := p.parseExpr()
	switch p.peek().Type {
	case lexer.TokenEq, lexer.TokenNeq, lexer.TokenGt, lexer.TokenGte, lexer.TokenLt, lexer.TokenLte:
		op := p.advance().Value
		right := p.parseExpr()
		return &ast.CompareCondition{Left: expr, Op: op, Right: right}

	case lexer.TokenIn:
		p.advance()
		members := p.parseMembersList()
		return &ast.MembershipCondition{Expr: expr, Negated: false, Members: members}

	case lexer.TokenNot:
		p.advance() // not
		p.expect(lexer.TokenIn)
		members := p.parseMembersList()
		return &ast.MembershipCondition{Expr: expr, Negated: true, Members: members}

	case lexer.TokenContains:
		p.advance()
		val := p.expectString()
		return &ast.StringMatchCondition{Subject: expr, Op: "contains", Value: val}

	case lexer.TokenStartsWith:
		p.advance()
		val := p.expectString()
		return &ast.StringMatchCondition{Subject: expr, Op: "starts_with", Value: val}

	case lexer.TokenEndsWith:
		p.advance()
		val := p.expectString()
		return &ast.StringMatchCondition{Subject: expr, Op: "ends_with", Value: val}

	case lexer.TokenIs:
		p.advance()
		if p.at(lexer.TokenAnomaly) {
			p.advance()
			p.expect(lexer.TokenComparedTo)
			p.expect(lexer.TokenLast)
			n, _ := strconv.Atoi(p.expectNumberStr())
			unit := p.expectDurationUnit()
			return &ast.AnomalyCondition{Subject: expr, Window: ast.Duration{Value: n, Unit: unit}}
		}
		name := p.expectString()
		return &ast.IsCondition{Subject: expr, Name: name}

	case lexer.TokenOlderThan:
		p.advance()
		dur := p.parseDuration()
		return &ast.TemporalCondition{Subject: expr, Op: "older_than", Value: dur}

	case lexer.TokenNewerThan:
		p.advance()
		dur := p.parseDuration()
		return &ast.TemporalCondition{Subject: expr, Op: "newer_than", Value: dur}

	case lexer.TokenChangedTo:
		p.advance()
		val := p.parseExpr()
		return &ast.ChangedToCondition{Attribute: exprToAttrName(expr), Value: val}

	default:
		p.errorf("expected condition operator after expression, got %q", p.peek().Value)
		return nil
	}
}

func (p *parser) parseMembersList() []ast.Expr {
	if p.at(lexer.TokenLBracket) {
		p.advance()
		var members []ast.Expr
		for !p.at(lexer.TokenRBracket) && !p.at(lexer.TokenEOF) {
			members = append(members, p.parseExpr())
			if p.at(lexer.TokenComma) {
				p.advance()
			}
		}
		p.expect(lexer.TokenRBracket)
		return members
	}
	// category_tree("Root")
	if p.at(lexer.TokenCategoryTree) {
		return []ast.Expr{p.parsePrimary()}
	}
	p.errorf("expected '[' or category_tree() in membership condition")
	return nil
}

func (p *parser) parseDuration() ast.Duration {
	n, _ := strconv.Atoi(p.expectNumberStr())
	unit := p.expectDurationUnit()
	return ast.Duration{Value: n, Unit: unit}
}

// ─── Expression parsing ───────────────────────────────────────────────────────

func (p *parser) parseExpr() ast.Expr {
	return p.parseAddSub()
}

func (p *parser) parseAddSub() ast.Expr {
	left := p.parseMulDiv()
	for p.at(lexer.TokenPlus) || p.at(lexer.TokenMinus) {
		op := p.advance().Value
		right := p.parseMulDiv()
		left = &ast.BinaryExpr{Left: left, Op: op, Right: right}
	}
	return left
}

func (p *parser) parseMulDiv() ast.Expr {
	left := p.parseUnary()
	for p.at(lexer.TokenStar) || p.at(lexer.TokenSlash) || p.at(lexer.TokenPercent) {
		op := p.advance().Value
		right := p.parseUnary()
		left = &ast.BinaryExpr{Left: left, Op: op, Right: right}
	}
	return left
}

func (p *parser) parseUnary() ast.Expr {
	if p.at(lexer.TokenMinus) {
		p.advance()
		return &ast.UnaryExpr{Op: "-", Operand: p.parsePrimary()}
	}
	return p.parsePrimary()
}

func (p *parser) parsePrimary() ast.Expr {
	switch p.peek().Type {
	case lexer.TokenAttr:
		p.advance()
		name := p.expectString()
		return &ast.AttrExpr{Name: name}

	case lexer.TokenContext:
		p.advance()
		p.expect(lexer.TokenDot)
		field := p.expectIdent()
		for p.at(lexer.TokenDot) {
			p.advance()
			field += "." + p.expectIdent()
		}
		return &ast.ContextExpr{Field: field}

	case lexer.TokenStep:
		p.advance()
		p.expect(lexer.TokenLParen)
		name := p.expectString()
		p.expect(lexer.TokenRParen)
		p.expect(lexer.TokenDot)
		field := p.expectIdent()
		for p.at(lexer.TokenDot) {
			p.advance()
			next := p.expectIdent()
			if next == "map" && p.at(lexer.TokenLParen) {
				p.advance() // consume (
				mapField := p.expectIdent()
				p.expect(lexer.TokenRParen)
				return &ast.MapExpr{
					Source: &ast.StepResultExpr{StepName: name, Field: field},
					Field:  mapField,
				}
			}
			field += "." + next
		}
		return &ast.StepResultExpr{StepName: name, Field: field}

	case lexer.TokenCategoryTree:
		p.advance()
		p.expect(lexer.TokenLParen)
		root := p.expectString()
		p.expect(lexer.TokenRParen)
		return &ast.CategoryTreeExpr{Root: root}

	case lexer.TokenToday:
		p.advance()
		return &ast.TodayExpr{}

	case lexer.TokenLearnedThreshold:
		p.advance()               // learned_threshold
		method := p.expectIdent() // e.g. "p95"
		if p.at(lexer.TokenIdent) && p.peek().Value == "of" {
			p.advance()
		} else {
			p.errorf("expected 'of' after learned_threshold %s, got %q", method, p.peek().Value)
		}
		subject := p.parsePrimary()
		p.expect(lexer.TokenOver)
		p.expect(lexer.TokenLast)
		n, _ := strconv.Atoi(p.expectNumberStr())
		unit := p.expectDurationUnit()
		return &ast.LearnedThresholdExpr{
			Method:  method,
			Subject: subject,
			Window:  ast.Duration{Value: n, Unit: unit},
		}

	case lexer.TokenNumber:
		val := p.advance().Value
		n, _ := strconv.ParseFloat(val, 64)
		return &ast.LiteralExpr{Value: n}

	case lexer.TokenString:
		val := p.advance().Value
		return &ast.LiteralExpr{Value: val}

	case lexer.TokenBool:
		val := p.advance().Value
		return &ast.LiteralExpr{Value: val == "true"}

	case lexer.TokenLBracket:
		p.advance()
		var elems []ast.Expr
		for !p.at(lexer.TokenRBracket) && !p.at(lexer.TokenEOF) {
			elems = append(elems, p.parseExpr())
			if p.at(lexer.TokenComma) {
				p.advance()
			}
		}
		p.expect(lexer.TokenRBracket)
		return &ast.ListExpr{Elements: elems}

	case lexer.TokenLParen:
		p.advance()
		expr := p.parseExpr()
		p.expect(lexer.TokenRParen)
		return expr

	case lexer.TokenTotal, lexer.TokenCount, lexer.TokenAvg:
		fn := p.advance().Value
		p.expect(lexer.TokenLParen)
		var arg ast.Expr
		// `count(records)` or `count()` is valid; otherwise expression required.
		if p.at(lexer.TokenRecords) || p.at(lexer.TokenRParen) {
			if p.at(lexer.TokenRecords) {
				p.advance()
			}
		} else {
			arg = p.parseExpr()
		}
		p.expect(lexer.TokenRParen)
		return &ast.AggregateExpr{Fn: fn, Arg: arg}

	// Keywords used as bare attribute names
	case lexer.TokenStatus, lexer.TokenCategory, lexer.TokenTypeKw,
		lexer.TokenIdent, lexer.TokenRecords:
		name := p.advance().Value
		// handle "tool_arg STRING" → treat as attr expr
		if name == "tool_arg" && p.at(lexer.TokenString) {
			argName := p.advance().Value
			return &ast.AttrExpr{Name: argName}
		}
		return &ast.IdentExpr{Name: name}

	default:
		p.errorf("expected expression, got %q", p.peek().Value)
		return &ast.LiteralExpr{Value: nil}
	}
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

func (p *parser) peek() lexer.Token {
	return p.tokens[p.pos]
}

func (p *parser) at(tt lexer.TokenType) bool {
	return p.tokens[p.pos].Type == tt
}

func (p *parser) atEOF() bool {
	return p.at(lexer.TokenEOF)
}

func (p *parser) advance() lexer.Token {
	t := p.tokens[p.pos]
	if !p.atEOF() {
		p.pos++
	}
	return t
}

func (p *parser) expect(tt lexer.TokenType) bool {
	if p.at(tt) {
		p.advance()
		return true
	}
	p.errorf("expected token %d, got %q", tt, p.peek().Value)
	return false
}

func (p *parser) expectString() string {
	if p.at(lexer.TokenString) {
		return p.advance().Value
	}
	p.errorf("expected string literal, got %q", p.peek().Value)
	return ""
}

func (p *parser) expectNumberStr() string {
	if p.at(lexer.TokenNumber) {
		return p.advance().Value
	}
	p.errorf("expected number, got %q", p.peek().Value)
	return "0"
}

func (p *parser) expectIdent() string {
	switch p.peek().Type {
	case lexer.TokenIdent:
		return p.advance().Value
	// Allow certain keywords to act as identifiers
	case lexer.TokenStatus, lexer.TokenCategory, lexer.TokenTypeKw,
		lexer.TokenFrom, lexer.TokenRecords, lexer.TokenItems,
		lexer.TokenAction:
		return p.advance().Value
	}
	p.errorf("expected identifier, got %q", p.peek().Value)
	return ""
}

func (p *parser) expectDurationUnit() string {
	switch p.peek().Type {
	case lexer.TokenDays:
		return p.advance().Value
	case lexer.TokenWeeks:
		return p.advance().Value
	case lexer.TokenMonths:
		return p.advance().Value
	case lexer.TokenYears:
		return p.advance().Value
	case lexer.TokenKm:
		return p.advance().Value
	case lexer.TokenIdent:
		return p.advance().Value
	}
	p.errorf("expected duration unit (days/weeks/months/years/km), got %q", p.peek().Value)
	return "days"
}

func (p *parser) peekDurationUnitOrIdent() string {
	switch p.peek().Type {
	case lexer.TokenDays, lexer.TokenWeeks, lexer.TokenMonths, lexer.TokenYears, lexer.TokenKm:
		return p.advance().Value
	case lexer.TokenIdent:
		return p.advance().Value
	}
	p.errorf("expected duration unit or identifier, got %q", p.peek().Value)
	return ""
}

func (p *parser) errorf(format string, args ...interface{}) {
	tok := p.peek()
	p.diags.AddError(p.file, tok.Line, tok.Col, fmt.Sprintf(format, args...), "")
}

// synchronize skips to the next top-level `}` to recover from a block-level error.
func (p *parser) synchronize() {
	depth := 0
	for !p.atEOF() {
		switch p.peek().Type {
		case lexer.TokenLBrace:
			depth++
			p.advance()
		case lexer.TokenRBrace:
			if depth == 0 {
				p.advance()
				return
			}
			depth--
			p.advance()
		default:
			p.advance()
		}
	}
}

// synchronizeInBlock skips to the next clause start within a block body.
// Used for unknown tokens inside a block — advances one token.
func (p *parser) synchronizeInBlock() {
	p.advance()
}

// ─── test block ────────────────────────────────────────────────────────────

func (p *parser) parseTestBlock() *ast.TestBlock {
	tok := p.advance() // test
	name := p.expectString()
	if !p.expect(lexer.TokenLBrace) {
		p.synchronize()
		return nil
	}
	b := &ast.TestBlock{Name: name, Pos: ast.Pos{Line: tok.Line, Col: tok.Col}}

	for !p.at(lexer.TokenRBrace) && !p.at(lexer.TokenEOF) {
		switch p.peek().Type {
		case lexer.TokenGiven:
			p.advance() // given
			p.expect(lexer.TokenLBrace)
			for !p.at(lexer.TokenRBrace) && !p.at(lexer.TokenEOF) {
				b.Given = append(b.Given, p.parseTestDatum())
			}
			p.expect(lexer.TokenRBrace)
		case lexer.TokenWhen:
			p.advance()                    // when
			b.WhenKind = p.advance().Value // detect, rule, forecast, etc.
			b.WhenBlock = p.expectString()
		case lexer.TokenExpect:
			p.advance() // expect
			p.expect(lexer.TokenLBrace)
			for !p.at(lexer.TokenRBrace) && !p.at(lexer.TokenEOF) {
				b.Expect = append(b.Expect, p.parseTestAssertion())
			}
			p.expect(lexer.TokenRBrace)
		default:
			p.errorf("unexpected token %q inside test block", p.peek().Value)
			p.synchronizeInBlock()
		}
	}
	p.expect(lexer.TokenRBrace)
	return b
}

func (p *parser) parseTestDatum() ast.TestDatum {
	switch p.peek().Type {
	case lexer.TokenRecord:
		p.advance() // record
		id, _ := strconv.Atoi(p.expectNumberStr())
		fields := map[string]interface{}{}
		// Parse key value pairs: type "item" category "Vehicles" status "active"
		for !p.at(lexer.TokenRBrace) && !p.at(lexer.TokenEOF) &&
			!p.at(lexer.TokenRecord) && !p.at(lexer.TokenAttr) {
			key := p.advance().Value // type, category, status, or ident
			switch p.peek().Type {
			case lexer.TokenString:
				fields[key] = p.advance().Value
			case lexer.TokenNumber:
				n, _ := strconv.ParseFloat(p.advance().Value, 64)
				fields[key] = n
			case lexer.TokenBool:
				fields[key] = p.advance().Value == "true"
			default:
				p.errorf("expected value after %q in record, got %q", key, p.peek().Value)
				p.advance()
			}
		}
		return ast.TestDatum{Kind: "record", ID: id, Fields: fields}

	case lexer.TokenAttr:
		p.advance() // attr
		id, _ := strconv.Atoi(p.expectNumberStr())
		attrName := p.expectString()
		fields := map[string]interface{}{}
		switch p.peek().Type {
		case lexer.TokenString:
			fields[attrName] = p.advance().Value
		case lexer.TokenNumber:
			n, _ := strconv.ParseFloat(p.advance().Value, 64)
			fields[attrName] = n
		case lexer.TokenBool:
			fields[attrName] = p.advance().Value == "true"
		default:
			p.errorf("expected value for attr %q, got %q", attrName, p.peek().Value)
		}
		return ast.TestDatum{Kind: "attr", ID: id, Fields: fields}

	default:
		p.errorf("expected 'record' or 'attr' inside given block, got %q", p.peek().Value)
		p.advance()
		return ast.TestDatum{}
	}
}

func (p *parser) parseTestAssertion() ast.TestAssertion {
	switch p.peek().Type {
	case lexer.TokenFlagged:
		p.advance() // flagged
		id, _ := strconv.Atoi(p.expectNumberStr())
		return ast.TestAssertion{Kind: "flagged", ID: id}

	case lexer.TokenNot:
		p.advance() // not
		if p.at(lexer.TokenFlagged) {
			p.advance() // flagged
			id, _ := strconv.Atoi(p.expectNumberStr())
			return ast.TestAssertion{Kind: "not_flagged", ID: id}
		}
		p.errorf("expected 'flagged' after 'not' in expect, got %q", p.peek().Value)
		p.advance()
		return ast.TestAssertion{}

	case lexer.TokenLabel:
		p.advance()             // label
		op := p.advance().Value // "contains", "==", etc.
		val := p.expectString()
		return ast.TestAssertion{Kind: "label", Op: op, Value: val}

	case lexer.TokenPriority:
		p.advance()              // priority
		op := p.advance().Value  // "=="
		val := p.advance().Value // HIGH, LOW, etc.
		return ast.TestAssertion{Kind: "priority", Op: op, Value: val}

	case lexer.TokenThreshold:
		p.advance()             // threshold
		op := p.advance().Value // "~=", ">", etc.
		val := p.advance().Value
		return ast.TestAssertion{Kind: "threshold", Op: op, Value: val}

	case lexer.TokenIdent:
		// e.g. "count == 2" or "score 808 > 2.5"
		kind := p.advance().Value
		if kind == "score" {
			id, _ := strconv.Atoi(p.expectNumberStr())
			op := p.advance().Value
			val := p.advance().Value
			return ast.TestAssertion{Kind: "score", ID: id, Op: op, Value: val}
		}
		op := p.advance().Value
		val := p.advance().Value
		return ast.TestAssertion{Kind: kind, Op: op, Value: val}

	default:
		p.errorf("unexpected token %q inside expect block", p.peek().Value)
		p.advance()
		return ast.TestAssertion{}
	}
}

// exprToAttrName extracts a string attribute name from an expression node.
func exprToAttrName(e ast.Expr) string {
	switch v := e.(type) {
	case *ast.IdentExpr:
		return v.Name
	case *ast.AttrExpr:
		return v.Name
	}
	return ""
}
