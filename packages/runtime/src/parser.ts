import { Token, TokenType } from "./lexer"
import type {
  Program,
  Rule,
  Define,
  Action,
  Condition,
  Expr,
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
      } else {
        this.advance() // skip unknown
      }
    }

    return { rules, defines }
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
      } else if (this.at(TokenType.Do)) {
        this.advance()
        actions.push(this.parseAction())
      } else {
        this.advance() // skip unknown
      }
    }
    this.expect(TokenType.RBrace)

    return { name, when, actions }
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
    // VERB args... (until next `do` or `}`)
    const verb = this.advance().value
    const args: Expr[] = []

    while (
      !this.at(TokenType.Do) &&
      !this.at(TokenType.RBrace) &&
      !this.at(TokenType.When) &&
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

    return { verb, args }
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
      return { type: "path", value: tok.value }
    }

    // Fallback
    this.advance()
    return { type: "literal", value: tok.value }
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
