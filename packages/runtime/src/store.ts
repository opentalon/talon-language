import { lex } from "./lexer"
import { parse } from "./parser"
import { evaluate, resolveExpr, resolvePath } from "./evaluator"
import type { Rule, Define, Action, EvalContext } from "./ast"
export type { EvalContext } from "./evaluator"

export interface ActionResult {
  rule: string
  verb: string
  args: any[]
}

export type ActionHandler = (...args: any[]) => void
export type Subscriber = (actions: ActionResult[]) => void
export type Unsubscribe = () => void

export class TalonStore {
  private state = new Map<string, any>()
  private ruleSets = new Map<string, Rule[]>()
  private defines = new Map<string, Define>()
  private actionHandlers = new Map<string, ActionHandler>()
  private subscribers: Array<{ prefix: string; cb: Subscriber }> = []

  // ─── Rules ─────────────────────────────────────────────

  loadRules(source: string, opts?: { tag?: string }): void {
    const tag = opts?.tag ?? "_default"
    const tokens = lex(source)
    const program = parse(tokens)

    // Store defines globally
    for (const d of program.defines) {
      this.defines.set(d.name, d)
    }

    // Store rules by tag
    const existing = this.ruleSets.get(tag) ?? []
    this.ruleSets.set(tag, [...existing, ...program.rules])

    // Don't auto-evaluate on load — wait for first set()
  }

  unloadRules(tag: string): void {
    this.ruleSets.delete(tag)
  }

  // ─── State ─────────────────────────────────────────────

  set(path: string, value: any): void {
    this.state.set(path, value)
    this.evaluateAll(new Set([path]))
  }

  get(path: string): any {
    return resolvePath(path, this.state)
  }

  // ─── Extension ─────────────────────────────────────────

  registerAction(verb: string, handler: ActionHandler): void {
    this.actionHandlers.set(verb, handler)
  }

  // ─── Subscription ──────────────────────────────────────

  subscribe(prefix: string, cb: Subscriber): Unsubscribe {
    const entry = { prefix, cb }
    this.subscribers.push(entry)
    return () => {
      const idx = this.subscribers.indexOf(entry)
      if (idx >= 0) this.subscribers.splice(idx, 1)
    }
  }

  // ─── Evaluation ────────────────────────────────────────

  private evaluateAll(changedPaths: Set<string>): void {
    const ctx: EvalContext = {
      state: this.state,
      defines: this.defines,
      changedPaths,
    }

    const allActions: ActionResult[] = []

    for (const rules of this.ruleSets.values()) {
      for (const rule of rules) {
        // If rule has no when clause, always fires
        const matches = rule.when === null || evaluate(rule.when, ctx)
        if (!matches) continue

        for (const action of rule.actions) {
          const result = this.executeAction(rule.name, action, ctx)
          if (result) allActions.push(result)
        }
      }
    }

    // Notify subscribers
    this.notifySubscribers(allActions)
  }

  private executeAction(
    ruleName: string,
    action: Action,
    ctx: EvalContext
  ): ActionResult | null {
    // Built-in: "set" — update store state
    if (action.verb === "set" && action.args.length >= 2) {
      const path = action.args[0].type === "path" ? action.args[0].value : String(resolveExpr(action.args[0], ctx))
      const value = resolveExpr(action.args[action.args.length - 1], ctx)
      this.state.set(path, value)
      return { rule: ruleName, verb: "set", args: [path, value] }
    }

    // Built-in: "validate" — check pattern/min/max
    if (action.verb === "validate") {
      return this.executeValidate(ruleName, action, ctx)
    }

    // App-registered action — pass raw path strings for path args, resolved values for literals
    const handlerArgs = action.args.map((arg) =>
      arg.type === "path" ? arg.value : resolveExpr(arg, ctx)
    )

    const handler = this.actionHandlers.get(action.verb)
    if (handler) {
      handler(...handlerArgs)
    }

    return { rule: ruleName, verb: action.verb, args: handlerArgs }
  }

  private executeValidate(
    ruleName: string,
    action: Action,
    ctx: EvalContext
  ): ActionResult {
    const path =
      action.args[0]?.type === "path" ? action.args[0].value : String(resolveExpr(action.args[0], ctx))
    const currentValue = resolvePath(path, this.state)
    let valid = true
    let message = ""

    // Args after the path are validation params.
    // Parser strips keywords (pattern/min/max) and leaves values.
    // e.g. validate "email" pattern ".+@.+" → args: [PathExpr("email"), PathExpr(".+@.+")]
    // e.g. validate "qty" min 1 max 100 → args: [PathExpr("qty"), Literal("min"), Literal(1), Literal("max"), Literal(100)]
    for (let i = 1; i < action.args.length; i++) {
      const arg = action.args[i]
      const rawValue = arg.type === "path" ? arg.value : arg.type === "literal" ? arg.value : null

      // Check if this is a min/max keyword marker
      if (rawValue === "min" && i + 1 < action.args.length) {
        const minVal = resolveExpr(action.args[++i], ctx) as number
        if (currentValue == null || Number(currentValue) < minVal) {
          valid = false
          message = `must be at least ${minVal}`
        }
      } else if (rawValue === "max" && i + 1 < action.args.length) {
        const maxVal = resolveExpr(action.args[++i], ctx) as number
        if (currentValue != null && Number(currentValue) > maxVal) {
          valid = false
          message = `must be at most ${maxVal}`
        }
      } else if (typeof rawValue === "string" && rawValue !== "min" && rawValue !== "max") {
        // Treat as regex pattern
        try {
          if (currentValue == null || !new RegExp(rawValue).test(String(currentValue))) {
            valid = false
            message = `does not match pattern ${rawValue}`
          }
        } catch {
          valid = false
          message = `invalid pattern: ${rawValue}`
        }
      }
    }

    return { rule: ruleName, verb: "validate", args: [path, { valid, message, value: currentValue }] }
  }

  private notifySubscribers(actions: ActionResult[]): void {
    for (const sub of this.subscribers) {
      // Filter actions relevant to this subscriber's prefix
      const prefix = sub.prefix
      if (prefix === "*") {
        sub.cb(actions)
        continue
      }

      const base = prefix.endsWith(".*") ? prefix.slice(0, -2) : prefix
      const relevant = actions.filter((a) => {
        // Check if any arg is a path starting with the prefix
        for (const arg of a.args) {
          if (typeof arg === "string" && arg.startsWith(base)) return true
        }
        return false
      })

      if (relevant.length > 0) {
        sub.cb(relevant)
      }
    }
  }
}
