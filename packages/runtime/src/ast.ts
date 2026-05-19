export interface Rule {
  name: string
  when: Condition | null
  actions: Action[]
}

export interface Define {
  name: string
  conditions: Condition[]
}

export interface Action {
  verb: string
  args: Expr[]
}

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

export type Expr = PathExpr | LiteralExpr | BinaryExpr

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
