import { describe, it, expect } from "vitest"
import { lex } from "../src/lexer"
import { parse } from "../src/parser"
import { TalonStore } from "../src/store"

describe("modules — reactive runtime", () => {
  it("namespaces exported rules and defines as ns.name", () => {
    const prog = parse(
      lex(`
      module "fleet.ui" {
        export define "at_risk" { "risk" == "high" }
        export rule "Warn" {
          when is "fleet.ui.at_risk"
          do show "warning.banner"
        }
      }
    `)
    )
    expect(prog.defines.map((d) => d.name)).toContain("fleet.ui.at_risk")
    expect(prog.rules.map((r) => r.name)).toContain("fleet.ui.Warn")
  })

  it("skips Go-only model blocks without choking on the surrounding rules", () => {
    const prog = parse(
      lex(`
      model "failure_risk" {
        classify knn k 3
        fitted { example [50000, 8] label "high" }
      }
      rule "Still parses" {
        when "x" changes
        do noop
      }
    `)
    )
    expect(prog.rules.map((r) => r.name)).toEqual(["Still parses"])
  })

  it("a namespaced define resolves at runtime via its qualified name", () => {
    const store = new TalonStore()
    const fired: any[] = []
    store.loadRules(`
      module "fleet.ui" {
        export define "at_risk" { "risk" == "high" }
        export rule "Warn" {
          when is "fleet.ui.at_risk"
          do show "warning.banner"
        }
      }
    `)
    store.subscribe("*", (a) => fired.push(...a))
    store.set("risk", "high")
    expect(fired.some((a) => a.verb === "show" && a.args.includes("warning.banner"))).toBe(true)
  })
})
