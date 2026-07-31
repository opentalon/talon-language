import { describe, it, expect } from "vitest"
import { TalonStore } from "../src/store"
import { lex } from "../src/lexer"
import { parse } from "../src/parser"

describe("control flow — parsing", () => {
  it("parses if / else-if / else, for-each, and while", () => {
    const prog = parse(
      lex(`
      rule "Escalate" {
        when "level" changes
        if "level" == "critical" {
          do page "ops"
        } else if "level" == "high" {
          do ticket "ops"
        } else {
          do log "ok"
        }
        for each channel in ["fleet-ops", "maintenance"] {
          do notify channel
        }
        while "queue.pending" > 0 {
          do drain "queue"
        }
      }
    `)
    )
    const actions = prog.rules[0].actions
    expect(actions).toHaveLength(3)

    expect(actions[0].type).toBe("if")
    if (actions[0].type === "if") {
      expect(actions[0].then).toHaveLength(1)
      // else-if nests a single IfAction in the else branch
      expect(actions[0].else).toHaveLength(1)
      expect(actions[0].else[0].type).toBe("if")
    }

    expect(actions[1].type).toBe("forEach")
    if (actions[1].type === "forEach") {
      expect(actions[1].variable).toBe("channel")
      expect(actions[1].over.type).toBe("list")
    }

    expect(actions[2].type).toBe("while")
    if (actions[2].type === "while") {
      expect(actions[2].maxIter).toBeGreaterThan(0)
    }
  })
})

describe("control flow — evaluation", () => {
  it("takes the then branch when the guard holds", () => {
    const store = new TalonStore()
    const fired: any[] = []
    store.loadRules(`
      rule "Severity" {
        when "priority" changes
        if "priority" == "critical" {
          do page "oncall"
        } else {
          do ticket "queue"
        }
      }
    `)
    store.subscribe("*", (a) => fired.push(...a))
    store.set("priority", "critical")
    expect(fired.map((a) => a.verb)).toEqual(["page"])
  })

  it("takes the else branch when the guard is false", () => {
    const store = new TalonStore()
    const fired: any[] = []
    store.loadRules(`
      rule "Severity" {
        when "priority" changes
        if "priority" == "critical" {
          do page "oncall"
        } else {
          do ticket "queue"
        }
      }
    `)
    store.subscribe("*", (a) => fired.push(...a))
    store.set("priority", "low")
    expect(fired.map((a) => a.verb)).toEqual(["ticket"])
  })

  it("iterates a list literal, binding the loop variable per element", () => {
    const store = new TalonStore()
    const fired: any[] = []
    store.loadRules(`
      rule "Fan out" {
        when "ping" changes
        for each channel in ["fleet-ops", "maintenance"] {
          do notify channel
        }
      }
    `)
    store.subscribe("*", (a) => fired.push(...a))
    store.set("ping", 1)
    const notifies = fired.filter((a) => a.verb === "notify")
    expect(notifies.map((a) => a.args[0])).toEqual(["fleet-ops", "maintenance"])
  })

  it("iterates over a store array path", () => {
    const store = new TalonStore()
    const fired: any[] = []
    store.set("channels", ["a", "b", "c"])
    store.loadRules(`
      rule "Fan out" {
        when "ping" changes
        for each ch in "channels" {
          do notify ch
        }
      }
    `)
    store.subscribe("*", (a) => fired.push(...a))
    store.set("ping", 1)
    const notifies = fired.filter((a) => a.verb === "notify")
    expect(notifies.map((a) => a.args[0])).toEqual(["a", "b", "c"])
  })

  it("runs while until the mutable guard flips, then stops", () => {
    const store = new TalonStore()
    const fired: any[] = []
    store.set("pending", 3)
    store.loadRules(`
      rule "Drain" {
        when "start" changes
        while "pending" > 0 {
          do log "drain"
          do set "pending" to "pending" - 1
        }
      }
    `)
    store.subscribe("*", (a) => fired.push(...a))
    store.set("start", 1)
    const logs = fired.filter((a) => a.verb === "log")
    expect(logs).toHaveLength(3) // pending 3 → 2 → 1 → 0
    expect(store.get("pending")).toBe(0)
  })
})
