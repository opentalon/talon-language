import { describe, it, expect } from "vitest"
import { evaluate, resolvePath } from "../src/evaluator"
import type { Condition, Define, EvalContext } from "../src/ast"

function ctx(
  state: Record<string, any>,
  defines?: Record<string, Define>,
  changedPaths?: string[]
): EvalContext {
  return {
    state: new Map(Object.entries(state)),
    defines: new Map(Object.entries(defines ?? {})),
    changedPaths: new Set(changedPaths ?? []),
  }
}

describe("evaluator", () => {
  it("evaluates equality", () => {
    const cond: Condition = {
      type: "compare",
      left: { type: "path", value: "product_type" },
      op: "==",
      right: { type: "literal", value: "digital" },
    }
    expect(evaluate(cond, ctx({ product_type: "digital" }))).toBe(true)
    expect(evaluate(cond, ctx({ product_type: "physical" }))).toBe(false)
  })

  it("evaluates inequality", () => {
    const cond: Condition = {
      type: "compare",
      left: { type: "path", value: "status" },
      op: "!=",
      right: { type: "literal", value: "closed" },
    }
    expect(evaluate(cond, ctx({ status: "active" }))).toBe(true)
    expect(evaluate(cond, ctx({ status: "closed" }))).toBe(false)
  })

  it("evaluates numeric comparison", () => {
    const cond: Condition = {
      type: "compare",
      left: { type: "path", value: "total" },
      op: ">",
      right: { type: "literal", value: 10000 },
    }
    expect(evaluate(cond, ctx({ total: 15000 }))).toBe(true)
    expect(evaluate(cond, ctx({ total: 5000 }))).toBe(false)
  })

  it("evaluates AND condition", () => {
    const cond: Condition = {
      type: "logical",
      op: "and",
      left: {
        type: "compare",
        left: { type: "path", value: "a" },
        op: "==",
        right: { type: "literal", value: 1 },
      },
      right: {
        type: "compare",
        left: { type: "path", value: "b" },
        op: "==",
        right: { type: "literal", value: 2 },
      },
    }
    expect(evaluate(cond, ctx({ a: 1, b: 2 }))).toBe(true)
    expect(evaluate(cond, ctx({ a: 1, b: 3 }))).toBe(false)
  })

  it("evaluates OR condition", () => {
    const cond: Condition = {
      type: "logical",
      op: "or",
      left: {
        type: "compare",
        left: { type: "path", value: "a" },
        op: "==",
        right: { type: "literal", value: 1 },
      },
      right: {
        type: "compare",
        left: { type: "path", value: "b" },
        op: "==",
        right: { type: "literal", value: 2 },
      },
    }
    expect(evaluate(cond, ctx({ a: 1, b: 99 }))).toBe(true)
    expect(evaluate(cond, ctx({ a: 99, b: 2 }))).toBe(true)
    expect(evaluate(cond, ctx({ a: 99, b: 99 }))).toBe(false)
  })

  it("evaluates NOT condition", () => {
    const cond: Condition = {
      type: "not",
      inner: {
        type: "compare",
        left: { type: "path", value: "status" },
        op: "==",
        right: { type: "literal", value: "active" },
      },
    }
    expect(evaluate(cond, ctx({ status: "inactive" }))).toBe(true)
    expect(evaluate(cond, ctx({ status: "active" }))).toBe(false)
  })

  it("evaluates changes condition", () => {
    const cond: Condition = { type: "changes", path: "booking.quantity" }
    expect(evaluate(cond, ctx({}, {}, ["booking.quantity"]))).toBe(true)
    expect(evaluate(cond, ctx({}, {}, ["booking.price"]))).toBe(false)
  })

  it("evaluates is condition (define reference)", () => {
    const cond: Condition = { type: "is", name: "high_value" }
    const defines = {
      high_value: {
        name: "high_value",
        conditions: [
          {
            type: "compare" as const,
            left: { type: "path" as const, value: "total" },
            op: ">",
            right: { type: "literal" as const, value: 10000 },
          },
        ],
      },
    }
    expect(evaluate(cond, ctx({ total: 15000 }, defines))).toBe(true)
    expect(evaluate(cond, ctx({ total: 5000 }, defines))).toBe(false)
  })
})

describe("resolvePath", () => {
  it("resolves direct key", () => {
    const state = new Map([["name", "Truck A"]])
    expect(resolvePath("name", state)).toBe("Truck A")
  })

  it("resolves nested object path", () => {
    const state = new Map<string, any>([
      ["booking", { payment: { iban: "DE89370400440532013000" } }],
    ])
    expect(resolvePath("booking.payment.iban", state)).toBe("DE89370400440532013000")
  })

  it("resolves array index path", () => {
    const state = new Map<string, any>([
      ["addons", [
        { name: "Warranty", price: 99 },
        { name: "Support", price: 199 },
      ]],
    ])
    expect(resolvePath("addons.0.name", state)).toBe("Warranty")
    expect(resolvePath("addons.1.price", state)).toBe(199)
  })

  it("returns undefined for missing path", () => {
    const state = new Map<string, any>()
    expect(resolvePath("missing.path", state)).toBeUndefined()
  })
})
