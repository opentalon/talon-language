export enum TokenType {
  // Literals
  String,
  Number,
  Bool,

  // Delimiters
  LBrace,
  RBrace,
  LBracket,
  RBracket,
  Comma,

  // Operators
  Eq,    // ==
  Neq,   // !=
  Lt,    // <
  Lte,   // <=
  Gt,    // >
  Gte,   // >=
  Plus,
  Minus,
  Star,
  Slash,

  // Keywords
  Rule,
  Define,
  When,
  Do,
  And,
  Or,
  Not,
  Is,
  To,
  From,
  Changes,
  Pattern,
  Min,
  Max,
  True,
  False,

  // Control flow
  If,
  Else,
  While,
  For,
  Each,
  In,

  // Special
  Ident,
  EOF,
}

export interface Token {
  type: TokenType
  value: string
  line: number
  col: number
}

const keywords: Record<string, TokenType> = {
  rule: TokenType.Rule,
  define: TokenType.Define,
  when: TokenType.When,
  do: TokenType.Do,
  and: TokenType.And,
  or: TokenType.Or,
  not: TokenType.Not,
  is: TokenType.Is,
  to: TokenType.To,
  from: TokenType.From,
  changes: TokenType.Changes,
  pattern: TokenType.Pattern,
  min: TokenType.Min,
  max: TokenType.Max,
  true: TokenType.True,
  false: TokenType.False,
  if: TokenType.If,
  else: TokenType.Else,
  while: TokenType.While,
  for: TokenType.For,
  each: TokenType.Each,
  in: TokenType.In,
}

export function lex(source: string): Token[] {
  const tokens: Token[] = []
  let pos = 0
  let line = 1
  let col = 1

  function peek(): string {
    return pos < source.length ? source[pos] : ""
  }

  function advance(): string {
    const ch = source[pos++]
    if (ch === "\n") {
      line++
      col = 1
    } else {
      col++
    }
    return ch
  }

  function skipWhitespace() {
    while (pos < source.length && /\s/.test(source[pos])) {
      advance()
    }
  }

  function skipComment() {
    if (source[pos] === "/" && source[pos + 1] === "/") {
      while (pos < source.length && source[pos] !== "\n") advance()
    }
  }

  while (pos < source.length) {
    skipWhitespace()
    if (pos >= source.length) break
    skipComment()
    if (pos >= source.length) break
    if (source[pos] === "/" && source[pos + 1] === "/") continue

    const startLine = line
    const startCol = col

    const ch = peek()

    if (ch === '"') {
      advance() // opening "
      let str = ""
      while (pos < source.length && source[pos] !== '"') {
        if (source[pos] === "\\") {
          advance()
          const esc = advance()
          if (esc === "n") str += "\n"
          else if (esc === "t") str += "\t"
          else if (esc === '"') str += '"'
          else if (esc === "\\") str += "\\"
          else str += esc
        } else {
          str += advance()
        }
      }
      if (pos < source.length) advance() // closing "
      tokens.push({ type: TokenType.String, value: str, line: startLine, col: startCol })
      continue
    }

    if (/[0-9]/.test(ch)) {
      let num = ""
      while (pos < source.length && /[0-9.]/.test(source[pos])) {
        num += advance()
      }
      tokens.push({ type: TokenType.Number, value: num, line: startLine, col: startCol })
      continue
    }

    if (/[a-zA-Z_]/.test(ch)) {
      let ident = ""
      while (pos < source.length && /[a-zA-Z0-9_]/.test(source[pos])) {
        ident += advance()
      }
      const kw = keywords[ident]
      if (kw !== undefined) {
        if (kw === TokenType.True || kw === TokenType.False) {
          tokens.push({ type: TokenType.Bool, value: ident, line: startLine, col: startCol })
        } else {
          tokens.push({ type: kw, value: ident, line: startLine, col: startCol })
        }
      } else {
        tokens.push({ type: TokenType.Ident, value: ident, line: startLine, col: startCol })
      }
      continue
    }

    // Symbols
    advance()
    switch (ch) {
      case "{":
        tokens.push({ type: TokenType.LBrace, value: ch, line: startLine, col: startCol })
        break
      case "}":
        tokens.push({ type: TokenType.RBrace, value: ch, line: startLine, col: startCol })
        break
      case "[":
        tokens.push({ type: TokenType.LBracket, value: ch, line: startLine, col: startCol })
        break
      case "]":
        tokens.push({ type: TokenType.RBracket, value: ch, line: startLine, col: startCol })
        break
      case ",":
        tokens.push({ type: TokenType.Comma, value: ch, line: startLine, col: startCol })
        break
      case "+":
        tokens.push({ type: TokenType.Plus, value: ch, line: startLine, col: startCol })
        break
      case "-":
        tokens.push({ type: TokenType.Minus, value: ch, line: startLine, col: startCol })
        break
      case "*":
        tokens.push({ type: TokenType.Star, value: ch, line: startLine, col: startCol })
        break
      case "/":
        tokens.push({ type: TokenType.Slash, value: ch, line: startLine, col: startCol })
        break
      case ">":
        if (peek() === "=") {
          advance()
          tokens.push({ type: TokenType.Gte, value: ">=", line: startLine, col: startCol })
        } else {
          tokens.push({ type: TokenType.Gt, value: ">", line: startLine, col: startCol })
        }
        break
      case "<":
        if (peek() === "=") {
          advance()
          tokens.push({ type: TokenType.Lte, value: "<=", line: startLine, col: startCol })
        } else {
          tokens.push({ type: TokenType.Lt, value: "<", line: startLine, col: startCol })
        }
        break
      case "=":
        if (peek() === "=") {
          advance()
          tokens.push({ type: TokenType.Eq, value: "==", line: startLine, col: startCol })
        }
        break
      case "!":
        if (peek() === "=") {
          advance()
          tokens.push({ type: TokenType.Neq, value: "!=", line: startLine, col: startCol })
        }
        break
    }
  }

  tokens.push({ type: TokenType.EOF, value: "", line, col })
  return tokens
}
