import type { Condition, Define, Expr } from "./ast"

export interface EvalContext {
  state: Map<string, any>
  defines: Map<string, Define>
  changedPaths: Set<string>
  // Loop variables bound by an enclosing `for each`. A path arg naming one
  // resolves to its value rather than being passed as a raw path string.
  loopVars?: Map<string, any>
}

export function evaluate(condition: Condition, ctx: EvalContext): boolean {
  switch (condition.type) {
    case "compare":
      return evalCompare(condition.left, condition.op, condition.right, ctx)
    case "logical":
      if (condition.op === "and") {
        return evaluate(condition.left, ctx) && evaluate(condition.right, ctx)
      }
      return evaluate(condition.left, ctx) || evaluate(condition.right, ctx)
    case "not":
      return !evaluate(condition.inner, ctx)
    case "changes":
      return ctx.changedPaths.has(condition.path)
    case "is": {
      const define = ctx.defines.get(condition.name)
      if (!define) return false
      return define.conditions.every((c) => evaluate(c, ctx))
    }
  }
}

function evalCompare(left: Expr, op: string, right: Expr, ctx: EvalContext): boolean {
  const lVal = resolveExpr(left, ctx)
  const rVal = resolveExpr(right, ctx)

  switch (op) {
    case "==":
      return lVal == rVal
    case "!=":
      return lVal != rVal
    case ">":
      return lVal > rVal
    case ">=":
      return lVal >= rVal
    case "<":
      return lVal < rVal
    case "<=":
      return lVal <= rVal
    default:
      return false
  }
}

export function resolveExpr(expr: Expr, ctx: EvalContext): any {
  switch (expr.type) {
    case "literal":
      return expr.value
    case "path": {
      const resolved = resolvePath(expr.value, ctx.state)
      // If path not found in state, treat the path string as a literal value.
      // This handles `"digital"` on the right side of comparisons —
      // it's a string literal, not a store path.
      return resolved !== undefined ? resolved : expr.value
    }
    case "binary":
      return evalBinary(expr.left, expr.op, expr.right, ctx)
    case "list":
      return expr.elements.map((el) => resolveExpr(el, ctx))
    case "call":
      return evalCall(expr.func, expr.args.map((a) => resolveExpr(a, ctx)))
  }
}

// evalCall dispatches the string builtins. Kept behaviourally in sync with
// the Go evaluator (internal/constraints): whole numbers stringify without a
// fractional part, substring is code-unit safe, split → array, join ← array.
function evalCall(func: string, args: any[]): any {
  const s = (v: any) =>
    typeof v === "number" && Number.isInteger(v) ? String(v) : v == null ? "" : String(v)
  switch (func) {
    case "upper":
      return s(args[0]).toUpperCase()
    case "lower":
      return s(args[0]).toLowerCase()
    case "trim":
      return s(args[0]).trim()
    case "length":
      return [...s(args[0])].length
    case "replace":
      return s(args[0]).split(s(args[1])).join(s(args[2])) // replace-all
    case "concat":
      return args.map(s).join("")
    case "substring": {
      const chars = [...s(args[0])]
      const start = Math.max(0, Math.min(Number(args[1]) | 0, chars.length))
      const end =
        args.length >= 3
          ? Math.max(start, Math.min(start + (Number(args[2]) | 0), chars.length))
          : chars.length
      return chars.slice(start, end).join("")
    }
    case "split":
      return s(args[0]).split(s(args[1]))
    case "join":
      return (Array.isArray(args[0]) ? args[0] : []).map(s).join(s(args[1]))
    default:
      return ""
  }
}

function evalBinary(left: Expr, op: string, right: Expr, ctx: EvalContext): any {
  const l = resolveExpr(left, ctx)
  const r = resolveExpr(right, ctx)

  switch (op) {
    case "+":
      return (l as number) + (r as number)
    case "-":
      return (l as number) - (r as number)
    case "*":
      return (l as number) * (r as number)
    case "/":
      return r !== 0 ? (l as number) / (r as number) : 0
    default:
      return 0
  }
}

export function resolvePath(path: string, state: Map<string, any>): any {
  // Direct match first
  if (state.has(path)) return state.get(path)

  // Nested resolution: "addons.1.price" → state["addons"][1]["price"]
  const parts = path.split(".")
  let current: any = state.get(parts[0])
  for (let i = 1; i < parts.length && current != null; i++) {
    const key = parts[i]
    if (Array.isArray(current)) {
      const idx = parseInt(key, 10)
      current = isNaN(idx) ? current[key as any] : current[idx]
    } else if (typeof current === "object") {
      current = current[key]
    } else {
      return undefined
    }
  }
  return current
}
