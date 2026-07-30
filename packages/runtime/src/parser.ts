import { Token, TokenType } from "./lexer"
import {
  DEFAULT_WHILE_MAX_ITER,
  STRING_BUILTINS,
  type Program,
  type Rule,
  type Define,
  type Action,
  type Condition,
  type Expr,
} from "./ast"

class Parser {
  private tokens: Token[]
  private pos = 0

  constructor(tokens: Token[]) {
    this.tokens = tokens
  }

  parse(): Program {
    const rules: Rule[] = []
    const defines: Define[] = []

    while (!this.at(TokenType.EOF)) {
      if (this.at(TokenType.Rule)) {
        rules.push(this.parseRule())
      } else if (this.at(TokenType.Define)) {
        defines.push(this.parseDefine())
      } else if (this.at(TokenType.Module)) {
        this.parseModule(rules, defines)
      } else if (this.at(TokenType.Model)) {
        // ML models are Go-only (the reactive runtime has no ML) — skip the
        // whole block so a shared source file still loads.
        this.skipBlock()
      } else {
        this.advance() // skip unknown
      }
    }

    return { rules, defines }
  }

  // parseModule flattens `module "ns" { export rule/define ... }` into the
  // program's rules/defines with namespaced names (ns.name), mirroring the Go
  // side. Exported `model` blocks are skipped (ML is Go-only). References use
  // the fully-qualified name, e.g. `is "ns.helper"`.
  private parseModule(rules: Rule[], defines: Define[]): void {
    this.advance() // module
    const ns = this.expectString()
    this.expect(TokenType.LBrace)
    while (!this.at(TokenType.RBrace) && !this.at(TokenType.EOF)) {
      if (this.at(TokenType.Export)) this.advance()
      if (this.at(TokenType.Rule)) {
        const r = this.parseRule()
        r.name = `${ns}.${r.name}`
        rules.push(r)
      } else if (this.at(TokenType.Define)) {
        const d = this.parseDefine()
        d.name = `${ns}.${d.name}`
        defines.push(d)
      } else if (this.at(TokenType.Model)) {
        this.skipBlock()
      } else {
        this.advance()
      }
    }
    this.expect(TokenType.RBrace)
  }

  // skipBlock consumes a block head (`model "x"`) and its balanced `{ ... }`
  // body without interpreting it.
  private skipBlock(): void {
    this.advance() // block keyword
    while (!this.at(TokenType.LBrace) && !this.at(TokenType.EOF)) this.advance()
    if (this.at(TokenType.EOF)) return
    let depth = 0
    do {
      if (this.at(TokenType.LBrace)) depth++
      else if (this.at(TokenType.RBrace)) depth--
      this.advance()
    } while (depth > 0 && !this.at(TokenType.EOF))
  }

  private parseRule(): Rule {
    this.advance() // rule
    const name = this.expectString()
    this.expect(TokenType.LBrace)

    let when: Condition | null = null
    const actions: Action[] = []

    while (!this.at(TokenType.RBrace) && !this.at(TokenType.EOF)) {
      if (this.at(TokenType.When)) {
        this.advance()
        when = this.parseOrCondition()
      } else {
        const action = this.parseActionStmt()
        if (action) actions.push(action)
      }
    }
    this.expect(TokenType.RBrace)

    return { name, when, actions }
  }

  // ─── Action bodies (control flow) ────────────────────────

  // parseActionStmt parses one statement of a rule/control-flow body: a leaf
  // `do VERB args`, or an if/else, for-each, or while form. Returns null on
  // an unrecognised token (after advancing) so the caller keeps progress.
  private parseActionStmt(): Action | null {
    if (this.at(TokenType.Do)) {
      this.advance()
      return this.parseAction()
    }
    if (this.at(TokenType.If)) return this.parseIfAction()
    if (this.at(TokenType.For)) return this.parseForEachAction()
    if (this.at(TokenType.While)) return this.parseWhileAction()
    this.advance() // skip unknown
    return null
  }

  // parseActionBody parses a `{ actionStmt* }` block.
  private parseActionBody(): Action[] {
    this.expect(TokenType.LBrace)
    const body: Action[] = []
    while (!this.at(TokenType.RBrace) && !this.at(TokenType.EOF)) {
      const action = this.parseActionStmt()
      if (action) body.push(action)
    }
    this.expect(TokenType.RBrace)
    return body
  }

  private parseIfAction(): Action {
    this.advance() // if
    const cond = this.parseOrCondition()
    const then = this.parseActionBody()
    let els: Action[] = []
    if (this.at(TokenType.Else)) {
      this.advance()
      // `else if` chains as a single nested IfAction.
      els = this.at(TokenType.If) ? [this.parseIfAction()] : this.parseActionBody()
    }
    return { type: "if", cond, then, else: els }
  }

  private parseForEachAction(): Action {
    this.advance() // for
    this.expect(TokenType.Each)
    const variable = this.advance().value
    this.expect(TokenType.In)
    const over = this.parseExpr()
    const body = this.parseActionBody()
    return { type: "forEach", variable, over, body }
  }

  private parseWhileAction(): Action {
    this.advance() // while
    const cond = this.parseOrCondition()
    const body = this.parseActionBody()
    return { type: "while", cond, body, maxIter: DEFAULT_WHILE_MAX_ITER }
  }

  private parseDefine(): Define {
    this.advance() // define
    const name = this.expectString()
    this.expect(TokenType.LBrace)

    const conditions: Condition[] = []
    while (!this.at(TokenType.RBrace) && !this.at(TokenType.EOF)) {
      conditions.push(this.parseOrCondition())
    }
    this.expect(TokenType.RBrace)

    return { name, conditions }
  }

  private parseAction(): Action {
    // VERB args... (until the next statement boundary)
    const verb = this.advance().value
    const args: Expr[] = []

    while (
      !this.at(TokenType.Do) &&
      !this.at(TokenType.RBrace) &&
      !this.at(TokenType.When) &&
      !this.at(TokenType.If) &&
      !this.at(TokenType.For) &&
      !this.at(TokenType.While) &&
      !this.at(TokenType.Else) &&
      !this.at(TokenType.EOF)
    ) {
      // Special: `to` separates target from expression in `set "X" to EXPR`
      if (this.at(TokenType.To)) {
        this.advance()
        args.push(this.parseExpr())
        continue
      }
      // Special: `from` for `load_options "X" from "source"`
      if (this.at(TokenType.From)) {
        this.advance()
        args.push(this.parseExpr())
        continue
      }
      // Special: `pattern` for `validate "X" pattern "regex"`
      if (this.at(TokenType.Pattern)) {
        this.advance()
        args.push(this.parsePrimary())
        continue
      }
      // Special: `min` / `max` for `validate "X" min N max M`
      if (this.at(TokenType.Min) || this.at(TokenType.Max)) {
        const kw = this.advance().value
        const val = this.parsePrimary()
        args.push({ type: "literal", value: kw } as Expr)
        args.push(val)
        continue
      }
      args.push(this.parseExpr())
    }

    return { type: "verb", verb, args }
  }

  // ─── Conditions ──────────────────────────────────────────

  private parseOrCondition(): Condition {
    let left = this.parseAndCondition()
    while (this.at(TokenType.Or)) {
      this.advance()
      const right = this.parseAndCondition()
      left = { type: "logical", op: "or", left, right }
    }
    return left
  }

  private parseAndCondition(): Condition {
    let left = this.parseNotCondition()
    while (this.at(TokenType.And)) {
      this.advance()
      const right = this.parseNotCondition()
      left = { type: "logical", op: "and", left, right }
    }
    return left
  }

  private parseNotCondition(): Condition {
    if (this.at(TokenType.Not)) {
      this.advance()
      return { type: "not", inner: this.parseAtomCondition() }
    }
    return this.parseAtomCondition()
  }

  private parseAtomCondition(): Condition {
    // `is "define_name"`
    if (this.at(TokenType.Is)) {
      this.advance()
      const name = this.expectString()
      return { type: "is", name }
    }

    // `"path" changes` or `"path" OP value`
    const left = this.parseExpr()

    if (this.at(TokenType.Changes)) {
      this.advance()
      if (left.type === "path") {
        return { type: "changes", path: left.value }
      }
      // fallback: treat literal as path
      if (left.type === "literal" && typeof left.value === "string") {
        return { type: "changes", path: left.value }
      }
    }

    // Comparison operators
    if (this.isCompareOp()) {
      const op = this.advance().value
      const right = this.parseExpr()
      return { type: "compare", left, op, right }
    }

    // Bare condition — shouldn't happen in valid input
    return { type: "compare", left, op: "==", right: { type: "literal", value: true } }
  }

  // ─── Expressions ─────────────────────────────────────────

  private parseExpr(): Expr {
    return this.parseAddSub()
  }

  private parseAddSub(): Expr {
    let left = this.parseMulDiv()
    while (this.at(TokenType.Plus) || this.at(TokenType.Minus)) {
      const op = this.advance().value
      const right = this.parseMulDiv()
      left = { type: "binary", left, op, right }
    }
    return left
  }

  private parseMulDiv(): Expr {
    let left = this.parsePrimary()
    while (this.at(TokenType.Star) || this.at(TokenType.Slash)) {
      const op = this.advance().value
      const right = this.parsePrimary()
      left = { type: "binary", left, op, right }
    }
    return left
  }

  private parsePrimary(): Expr {
    const tok = this.peek()

    // List literal: [ e, e, ... ] — the collection for `for each`.
    if (tok.type === TokenType.LBracket) {
      this.advance()
      const elements: Expr[] = []
      while (!this.at(TokenType.RBracket) && !this.at(TokenType.EOF)) {
        elements.push(this.parseExpr())
        if (this.at(TokenType.Comma)) this.advance()
      }
      this.expect(TokenType.RBracket)
      return { type: "list", elements }
    }

    if (tok.type === TokenType.String) {
      this.advance()
      // Strings in condition/expression context are path references
      return { type: "path", value: tok.value }
    }

    if (tok.type === TokenType.Number) {
      this.advance()
      return { type: "literal", value: parseFloat(tok.value) }
    }

    if (tok.type === TokenType.Bool) {
      this.advance()
      return { type: "literal", value: tok.value === "true" }
    }

    if (tok.type === TokenType.Ident) {
      this.advance()
      // `name(args...)` — a builtin string function call.
      if (this.at(TokenType.LParen) && STRING_BUILTINS.has(tok.value)) {
        return this.parseCallArgs(tok.value)
      }
      return { type: "path", value: tok.value }
    }

    // Fallback
    this.advance()
    return { type: "literal", value: tok.value }
  }

  private parseCallArgs(func: string): Expr {
    this.expect(TokenType.LParen)
    const args: Expr[] = []
    while (!this.at(TokenType.RParen) && !this.at(TokenType.EOF)) {
      args.push(this.parseExpr())
      if (this.at(TokenType.Comma)) this.advance()
    }
    this.expect(TokenType.RParen)
    return { type: "call", func, args }
  }

  // ─── Helpers ─────────────────────────────────────────────

  private peek(): Token {
    return this.tokens[this.pos]
  }

  private at(type: TokenType): boolean {
    return this.tokens[this.pos].type === type
  }

  private advance(): Token {
    const tok = this.tokens[this.pos]
    if (!this.at(TokenType.EOF)) this.pos++
    return tok
  }

  private expect(type: TokenType): Token {
    if (this.at(type)) return this.advance()
    throw new Error(
      `Expected token ${TokenType[type]}, got ${TokenType[this.peek().type]} ("${this.peek().value}") at line ${this.peek().line}`
    )
  }

  private expectString(): string {
    if (this.at(TokenType.String)) return this.advance().value
    throw new Error(`Expected string, got "${this.peek().value}" at line ${this.peek().line}`)
  }

  private isCompareOp(): boolean {
    const t = this.peek().type
    return (
      t === TokenType.Eq ||
      t === TokenType.Neq ||
      t === TokenType.Gt ||
      t === TokenType.Gte ||
      t === TokenType.Lt ||
      t === TokenType.Lte
    )
  }
}

export function parse(tokens: Token[]): Program {
  return new Parser(tokens).parse()
}
