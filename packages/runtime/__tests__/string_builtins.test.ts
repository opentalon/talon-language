import { describe, it, expect } from "vitest"
import { resolveExpr, type EvalContext } from "../src/evaluator"
import { lex } from "../src/lexer"
import { parse } from "../src/parser"
import { TlnStore } from "../src/store"

// evalExprString parses `<expr>` as the left side of a rule condition and
// evaluates it against the given state.
function evalExprString(expr: string, state: Record<string, any> = {}): any {
  const prog = parse(lex(`rule "T" { when ${expr} == "SENTINEL" do noop }`))
  const when = prog.rules[0].when as any
  const ctx: EvalContext = {
    state: new Map(Object.entries(state)),
    defines: new Map(),
    changedPaths: new Set(),
  }
  return resolveExpr(when.left, ctx)
}

describe("string builtins — evaluation", () => {
  const row = { vin: "1ftabc", sku: "AB1234", name: "  Broken  " }
  it("upper / lower / trim / length", () => {
    expect(evalExprString(`upper(vin)`, row)).toBe("1FTABC")
    expect(evalExprString(`lower(sku)`, row)).toBe("ab1234")
    expect(evalExprString(`trim(name)`, row)).toBe("Broken")
    expect(evalExprString(`length(sku)`, row)).toBe(6)
  })
  it("substring (2- and 3-arg) and nesting", () => {
    expect(evalExprString(`substring(sku, 0, 2)`, row)).toBe("AB")
    expect(evalExprString(`substring(sku, 2)`, row)).toBe("1234")
    expect(evalExprString(`upper(substring(vin, 0, 3))`, row)).toBe("1FT")
  })
  it("replace / concat / split / join", () => {
    expect(evalExprString(`replace(sku, "AB", "XY")`, row)).toBe("XY1234")
    expect(evalExprString(`concat("v-", vin, "-", length(sku))`, row)).toBe("v-1ftabc-6")
    expect(evalExprString(`join(split(sku, "1"), "_")`, row)).toBe("AB_234")
  })
})

describe("string builtins — in a rule action", () => {
  it("resolves a string function as an action argument", () => {
    const store = new TlnStore()
    const fired: any[] = []
    store.set("recall_code", "rb7")
    store.loadRules(`
      rule "Recall" {
        when "recall_code" changes
        do open_recall upper(recall_code)
      }
    `)
    store.subscribe("*", (a) => fired.push(...a))
    store.set("recall_code", "rb7")
    const call = fired.find((a) => a.verb === "open_recall")
    expect(call?.args[0]).toBe("RB7")
  })
})
