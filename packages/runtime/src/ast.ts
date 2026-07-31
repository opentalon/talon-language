export interface Rule {
  name: string
  when: Condition | null
  actions: Action[]
}

export interface Define {
  name: string
  conditions: Condition[]
}

// Action is a statement in a rule body: a leaf `do VERB args` (VerbAction)
// or one of the imperative control-flow forms — if/else, for-each, while.
// Mirrors the Go side's action bodies (issue #13). Control-flow guards reuse
// the Condition grammar, evaluated against the same store state.
export type Action = VerbAction | IfAction | ForEachAction | WhileAction

export interface VerbAction {
  type: "verb"
  verb: string
  args: Expr[]
}

export interface IfAction {
  type: "if"
  cond: Condition
  then: Action[]
  else: Action[] // [] when there is no else / else-if
}

export interface ForEachAction {
  type: "forEach"
  variable: string
  over: Expr // resolves to an array (a list literal or a store path)
  body: Action[]
}

export interface WhileAction {
  type: "while"
  cond: Condition
  body: Action[]
  maxIter: number // safety cap; the loop stops once reached
}

// DEFAULT_WHILE_MAX_ITER bounds a `while` when the source gives no explicit
// limit. Unlike the Go remediate runtime, the reactive store has mutable
// state (a `do set` can flip the guard), so a well-formed loop terminates
// on its own — this is only a backstop.
export const DEFAULT_WHILE_MAX_ITER = 10000

export type Condition =
  | CompareCondition
  | LogicalCondition
  | NotCondition
  | ChangesCondition
  | IsCondition

export interface CompareCondition {
  type: "compare"
  left: Expr
  op: string // "==", "!=", ">", "<", ">=", "<="
  right: Expr
}

export interface LogicalCondition {
  type: "logical"
  op: "and" | "or"
  left: Condition
  right: Condition
}

export interface NotCondition {
  type: "not"
  inner: Condition
}

export interface ChangesCondition {
  type: "changes"
  path: string
}

export interface IsCondition {
  type: "is"
  name: string
}

export type Expr = PathExpr | LiteralExpr | BinaryExpr | ListExpr | CallExpr

export interface ListExpr {
  type: "list"
  elements: Expr[]
}

// CallExpr is a builtin string function: upper/lower/trim/length/substring/
// replace/concat/split/join. Mirrors the Go side's CallExpr (issue #13).
export interface CallExpr {
  type: "call"
  func: string
  args: Expr[]
}

// STRING_BUILTINS are the function names parsed as CallExpr rather than a
// bare path. Kept in sync with the Go evaluator's string toolkit.
export const STRING_BUILTINS = new Set([
  "upper",
  "lower",
  "trim",
  "length",
  "substring",
  "replace",
  "concat",
  "split",
  "join",
])

export interface PathExpr {
  type: "path"
  value: string
}

export interface LiteralExpr {
  type: "literal"
  value: string | number | boolean
}

export interface BinaryExpr {
  type: "binary"
  left: Expr
  op: string // "+", "-", "*", "/"
  right: Expr
}

export interface Program {
  rules: Rule[]
  defines: Define[]
}
