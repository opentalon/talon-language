import { lex } from "./lexer"
import { parse } from "./parser"
import { evaluate, resolveExpr, resolvePath } from "./evaluator"
import type { Rule, Define, Action, VerbAction } from "./ast"
import type { EvalContext } from "./evaluator"
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

        this.executeActions(rule.name, rule.actions, ctx, allActions)
      }
    }

    // Notify subscribers
    this.notifySubscribers(allActions)
  }

  // executeActions walks an action body, dispatching leaf verbs and the
  // control-flow forms (if/else, for-each, while). Results from every
  // fired leaf accumulate into `acc`.
  private executeActions(
    ruleName: string,
    actions: Action[],
    ctx: EvalContext,
    acc: ActionResult[]
  ): void {
    for (const action of actions) {
      switch (action.type) {
        case "verb": {
          const result = this.executeVerb(ruleName, action, ctx)
          if (result) acc.push(result)
          break
        }
        case "if": {
          const branch = evaluate(action.cond, ctx) ? action.then : action.else
          this.executeActions(ruleName, branch, ctx, acc)
          break
        }
        case "forEach": {
          const over = resolveExpr(action.over, ctx)
          const items = Array.isArray(over) ? over : over == null ? [] : [over]
          if (!ctx.loopVars) ctx.loopVars = new Map()
          const hadVar = ctx.loopVars.has(action.variable)
          const savedVar = ctx.loopVars.get(action.variable)
          // Bind in state too so nested if/while guards resolve the variable.
          const hadState = this.state.has(action.variable)
          const savedState = this.state.get(action.variable)
          for (const item of items) {
            ctx.loopVars.set(action.variable, item)
            this.state.set(action.variable, item)
            this.executeActions(ruleName, action.body, ctx, acc)
          }
          if (hadVar) ctx.loopVars.set(action.variable, savedVar)
          else ctx.loopVars.delete(action.variable)
          if (hadState) this.state.set(action.variable, savedState)
          else this.state.delete(action.variable)
          break
        }
        case "while": {
          let iter = 0
          // The store has mutable state, so a `do set` inside the body can
          // flip the guard and terminate; maxIter is only a backstop.
          while (evaluate(action.cond, ctx)) {
            if (iter >= action.maxIter) break
            this.executeActions(ruleName, action.body, ctx, acc)
            iter++
          }
          break
        }
      }
    }
  }

  private executeVerb(
    ruleName: string,
    action: VerbAction,
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

    // App-registered action — pass raw path strings for path args, resolved
    // values for literals. A path naming a `for each` loop variable resolves
    // to its bound value (so `do notify channel` emits the element, not "channel").
    const handlerArgs = action.args.map((arg) =>
      arg.type === "path"
        ? ctx.loopVars?.has(arg.value)
          ? ctx.loopVars.get(arg.value)
          : arg.value
        : resolveExpr(arg, ctx)
    )

    const handler = this.actionHandlers.get(action.verb)
    if (handler) {
      handler(...handlerArgs)
    }

    return { rule: ruleName, verb: action.verb, args: handlerArgs }
  }

  private executeValidate(
    ruleName: string,
    action: VerbAction,
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
