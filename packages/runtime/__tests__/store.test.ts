import { describe, it, expect, vi } from "vitest"
import { TlnStore } from "../src/store"

describe("TlnStore", () => {
  it("loads rules and evaluates on set", () => {
    const store = new TlnStore()
    const actions: any[] = []

    store.loadRules(`
      rule "Show delivery" {
        when "product_type" != "digital"
        do show "delivery_address"
      }
    `)

    store.subscribe("*", (a) => actions.push(...a))
    store.set("product_type", "physical")

    expect(actions.some((a) => a.verb === "show" && a.args.includes("delivery_address"))).toBe(true)
  })

  it("does not fire rule when condition is false", () => {
    const store = new TlnStore()
    const actions: any[] = []

    store.loadRules(`
      rule "Show delivery" {
        when "product_type" != "digital"
        do show "delivery_address"
      }
    `)

    store.subscribe("*", (a) => actions.push(...a))
    store.set("product_type", "digital")

    expect(actions.some((a) => a.verb === "show")).toBe(false)
  })

  it("supports cross-form rules", () => {
    const store = new TlnStore()
    const bookingActions: any[] = []

    store.loadRules(`
      rule "Show booking details" {
        when "search.building_use" == "1"
        do show "booking.details"
      }
    `)

    store.subscribe("booking.*", (a) => bookingActions.push(...a))
    store.set("search.building_use", "1")

    expect(bookingActions).toHaveLength(1)
    expect(bookingActions[0].verb).toBe("show")
    expect(bookingActions[0].args).toContain("booking.details")
  })

  it("supports tagged rule loading and unloading", () => {
    const store = new TlnStore()
    const actions: any[] = []

    store.loadRules(`rule "R1" { when "a" == 1 do show "x" }`, { tag: "set1" })
    store.loadRules(`rule "R2" { when "a" == 1 do show "y" }`, { tag: "set2" })

    store.subscribe("*", (a) => {
      actions.length = 0
      actions.push(...a)
    })

    store.set("a", 1)
    expect(actions).toHaveLength(2)

    store.unloadRules("set2")
    store.set("a", 1) // re-trigger
    expect(actions).toHaveLength(1)
    expect(actions[0].args).toContain("x")
  })

  it("fires changes condition only when path changes", () => {
    const store = new TlnStore()
    const emitted: string[] = []

    store.registerAction("emit", (event: string) => emitted.push(event))
    store.loadRules(`
      rule "Recalculate" {
        when "booking.quantity" changes
        do emit "recalculate_price"
      }
    `)

    store.set("booking.quantity", 5)
    expect(emitted).toContain("recalculate_price")

    emitted.length = 0
    store.set("booking.name", "Test")
    expect(emitted).not.toContain("recalculate_price")
  })

  it("executes built-in set action", () => {
    const store = new TlnStore()

    store.loadRules(`
      rule "Calc subtotal" {
        when "unit_price" changes or "quantity" changes
        do set "subtotal" to "unit_price" * "quantity"
      }
    `)

    store.set("unit_price", 100)
    store.set("quantity", 5)

    expect(store.get("subtotal")).toBe(500)
  })

  it("executes validate action with pattern", () => {
    const store = new TlnStore()
    const actions: any[] = []

    store.loadRules(`
      rule "Validate email" {
        when "email" changes
        do validate "email" pattern ".+@.+"
      }
    `)

    store.subscribe("*", (a) => {
      actions.length = 0
      actions.push(...a)
    })

    store.set("email", "invalid")
    const validation = actions.find((a) => a.verb === "validate")
    expect(validation).toBeDefined()
    expect(validation.args[1].valid).toBe(false)

    store.set("email", "user@example.com")
    const valid = actions.find((a) => a.verb === "validate")
    expect(valid.args[1].valid).toBe(true)
  })

  it("calls registered action handlers", () => {
    const store = new TlnStore()
    const shown: string[] = []

    store.registerAction("show", (path: string) => shown.push(path))
    store.loadRules(`
      rule "Show it" {
        when "active" == true
        do show "panel"
      }
    `)

    store.set("active", true)
    expect(shown).toContain("panel")
  })

  it("supports define references", () => {
    const store = new TlnStore()
    const actions: any[] = []

    store.loadRules(`
      define "high_value" {
        "booking.total" > 10000
      }
      rule "Needs approval" {
        when is "high_value"
        do require "manager_approval"
      }
    `)

    store.subscribe("*", (a) => {
      actions.length = 0
      actions.push(...a)
    })

    store.set("booking.total", 5000)
    expect(actions.some((a) => a.verb === "require")).toBe(false)

    store.set("booking.total", 15000)
    expect(actions.some((a) => a.verb === "require")).toBe(true)
  })

  it("resolves nested data paths", () => {
    const store = new TlnStore()
    const actions: any[] = []

    store.loadRules(`
      rule "Addon selected" {
        when "addons.0.selected" == true
        do emit "addon_changed"
      }
    `)

    store.registerAction("emit", () => {})
    store.subscribe("*", (a) => {
      actions.length = 0
      actions.push(...a)
    })

    store.set("addons", [{ selected: false, name: "Warranty" }])
    expect(actions.some((a) => a.verb === "emit")).toBe(false)

    store.set("addons", [{ selected: true, name: "Warranty" }])
    expect(actions.some((a) => a.verb === "emit")).toBe(true)
  })

  it("subscriber prefix filters actions", () => {
    const store = new TlnStore()
    const searchActions: any[] = []
    const bookingActions: any[] = []

    store.loadRules(`
      rule "R1" { when "x" == 1 do show "search.field_a" }
      rule "R2" { when "x" == 1 do show "booking.field_b" }
    `)

    store.subscribe("search.*", (a) => { searchActions.length = 0; searchActions.push(...a) })
    store.subscribe("booking.*", (a) => { bookingActions.length = 0; bookingActions.push(...a) })

    store.set("x", 1)

    expect(searchActions).toHaveLength(1)
    expect(searchActions[0].args).toContain("search.field_a")
    expect(bookingActions).toHaveLength(1)
    expect(bookingActions[0].args).toContain("booking.field_b")
  })

  it("unsubscribe works", () => {
    const store = new TlnStore()
    const actions: any[] = []

    store.loadRules(`rule "R" { when "a" == 1 do show "x" }`)

    const unsub = store.subscribe("*", (a) => actions.push(...a))
    store.set("a", 1)
    expect(actions.length).toBeGreaterThan(0)

    actions.length = 0
    unsub()
    store.set("a", 1)
    expect(actions).toHaveLength(0)
  })
})
