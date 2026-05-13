import { describe, it, expect } from "vitest"
import { lex, TokenType } from "../src/lexer"

describe("lexer", () => {
  it("tokenizes a simple rule", () => {
    const tokens = lex(`rule "test" { }`)
    expect(tokens[0].type).toBe(TokenType.Rule)
    expect(tokens[1].type).toBe(TokenType.String)
    expect(tokens[1].value).toBe("test")
    expect(tokens[2].type).toBe(TokenType.LBrace)
    expect(tokens[3].type).toBe(TokenType.RBrace)
  })

  it("tokenizes comparison operators", () => {
    const tokens = lex(`== != > >= < <=`)
    expect(tokens.map((t) => t.value)).toEqual(["==", "!=", ">", ">=", "<", "<=", ""])
  })

  it("tokenizes keywords", () => {
    const tokens = lex(`when do and or not is to from changes define`)
    const types = tokens.slice(0, -1).map((t) => t.type)
    expect(types).toEqual([
      TokenType.When, TokenType.Do, TokenType.And, TokenType.Or,
      TokenType.Not, TokenType.Is, TokenType.To, TokenType.From,
      TokenType.Changes, TokenType.Define,
    ])
  })

  it("tokenizes numbers", () => {
    const tokens = lex(`42 3.14 0.19`)
    expect(tokens[0].value).toBe("42")
    expect(tokens[1].value).toBe("3.14")
    expect(tokens[2].value).toBe("0.19")
  })

  it("tokenizes booleans", () => {
    const tokens = lex(`true false`)
    expect(tokens[0].type).toBe(TokenType.Bool)
    expect(tokens[0].value).toBe("true")
    expect(tokens[1].type).toBe(TokenType.Bool)
    expect(tokens[1].value).toBe("false")
  })

  it("tokenizes string with escapes", () => {
    const tokens = lex(`"hello\\nworld"`)
    expect(tokens[0].value).toBe("hello\nworld")
  })

  it("skips line comments", () => {
    const tokens = lex(`// comment\nrule "test" { }`)
    expect(tokens[0].type).toBe(TokenType.Rule)
  })

  it("tokenizes arithmetic operators", () => {
    const tokens = lex(`+ - * /`)
    expect(tokens.map((t) => t.value)).toEqual(["+", "-", "*", "/", ""])
  })

  it("tokenizes identifiers", () => {
    const tokens = lex(`show hide require emit`)
    expect(tokens.slice(0, 4).every((t) => t.type === TokenType.Ident)).toBe(true)
  })
})
