import { describe, it, expect } from "vitest"
import { lex } from "../src/lexer"
import { parse } from "../src/parser"

function p(src: string) {
  return parse(lex(src))
}

describe("parser", () => {
  it("parses a simple rule with when and do", () => {
    const prog = p(`
      rule "Show delivery" {
        when "product_type" != "digital"
        do show "delivery_address"
      }
    `)
    expect(prog.rules).toHaveLength(1)
    expect(prog.rules[0].name).toBe("Show delivery")
    expect(prog.rules[0].when?.type).toBe("compare")
    expect(prog.rules[0].actions).toHaveLength(1)
    expect(prog.rules[0].actions[0].verb).toBe("show")
  })

  it("parses multiple do actions", () => {
    const prog = p(`
      rule "Multi action" {
        when "status" == "active"
        do show "field_a"
        do hide "field_b"
        do require "field_c"
      }
    `)
    expect(prog.rules[0].actions).toHaveLength(3)
    expect(prog.rules[0].actions.map((a) => a.verb)).toEqual(["show", "hide", "require"])
  })

  it("parses and/or conditions", () => {
    const prog = p(`
      rule "Complex" {
        when "a" == 1 and "b" == 2 or "c" == 3
        do emit "test"
      }
    `)
    const when = prog.rules[0].when!
    expect(when.type).toBe("logical")
  })

  it("parses changes condition", () => {
    const prog = p(`
      rule "On change" {
        when "booking.quantity" changes
        do emit "recalculate"
      }
    `)
    const when = prog.rules[0].when!
    expect(when.type).toBe("changes")
    if (when.type === "changes") {
      expect(when.path).toBe("booking.quantity")
    }
  })

  it("parses is condition (define reference)", () => {
    const prog = p(`
      define "high_value" {
        "booking.total" > 10000
      }
      rule "Approval needed" {
        when is "high_value"
        do require "manager_approval"
      }
    `)
    expect(prog.defines).toHaveLength(1)
    expect(prog.defines[0].name).toBe("high_value")
    expect(prog.rules[0].when?.type).toBe("is")
  })

  it("parses set with to", () => {
    const prog = p(`
      rule "Calc" {
        when "price" changes
        do set "total" to "price" * "quantity"
      }
    `)
    const action = prog.rules[0].actions[0]
    expect(action.verb).toBe("set")
    expect(action.args).toHaveLength(2)
    // First arg: path "total"
    expect(action.args[0]).toEqual({ type: "path", value: "total" })
    // Second arg: binary expression
    expect(action.args[1].type).toBe("binary")
  })

  it("parses validate with pattern", () => {
    const prog = p(`
      rule "Email check" {
        when "email" changes
        do validate "email" pattern ".+@.+"
      }
    `)
    const action = prog.rules[0].actions[0]
    expect(action.verb).toBe("validate")
    expect(action.args[0]).toEqual({ type: "path", value: "email" })
    // pattern arg is a path (string parsed as path)
    expect(action.args[1]).toEqual({ type: "path", value: ".+@.+" })
  })

  it("parses not condition", () => {
    const prog = p(`
      rule "Not active" {
        when not "status" == "active"
        do hide "panel"
      }
    `)
    expect(prog.rules[0].when?.type).toBe("not")
  })

  it("parses multiple rules and defines", () => {
    const prog = p(`
      define "premium" { "tier" == "gold" }
      rule "R1" { when "a" == 1 do show "x" }
      rule "R2" { when is "premium" do emit "vip" }
    `)
    expect(prog.defines).toHaveLength(1)
    expect(prog.rules).toHaveLength(2)
  })

  it("parses nested path references", () => {
    const prog = p(`
      rule "Addon" {
        when "addons.1.selected" == true
        do emit "recalculate"
      }
    `)
    const when = prog.rules[0].when!
    if (when.type === "compare") {
      expect(when.left).toEqual({ type: "path", value: "addons.1.selected" })
    }
  })
})
