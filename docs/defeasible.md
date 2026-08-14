# Defeasible reasoning

tln supports two kinds of rules: **strict** and **defeasible**.

- A **strict** rule cannot be defeated by anything. Use it for invariants
  that must always hold — safety, compliance, regulatory hard stops.
- A **defeasible** rule is the default unless a more specific or higher-
  priority rule overrides it.

This matters once rules accumulate from multiple sources (admin-shipped,
MCP-shipped, auto-discovered). Without explicit conflict resolution, the
last rule loaded silently wins. Defeasible reasoning gives you a
deterministic, debuggable answer.

## Syntax

```tln
// Strict — cannot be overridden, ever.
strict rule "Expired certification blocks assignment" {
  for records where type == "person"
  before "assign"
  block "assign"
  reason "Safety certification expired — non-negotiable"
}

// Defeasible default-deny.
rule "Block all deletions" {
  when tool_action contains "delete"
  block "delete"
  priority LOW
}

// More specific rule defeats the default.
rule "Cleanup crew can delete" {
  when tool_action contains "delete"
  overrides "Block all deletions"
  allow "delete"
  priority HIGH
}
```

`overrides` accepts one or more rule names: `overrides "A", "B"`.

## Conflict resolution

When multiple rules match the same target:

1. **Strict rules always fire.** They cannot be defeated and they do not
   participate in priority resolution.
2. **`overrides` edges defeat their targets.** If rule B overrides rule A
   and both match, A is suppressed. Override chains walk transitively.
3. **Highest priority wins among the survivors.** `CRITICAL > HIGH >
   MEDIUM > LOW`. Rules without an explicit `priority` default to MEDIUM.
4. **Unresolved ties surface a warning.** If two defeasible rules share
   the highest priority and neither overrides the other, both fire and
   the runtime warns you to disambiguate.

Source priorities recommended by issue #23:

| Source                       | Default priority         |
| ---------------------------- | ------------------------ |
| Strict rules (compliance)    | Cannot be overridden     |
| Tenant admin rules           | HIGH                     |
| MCP-shipped rules            | MEDIUM                   |
| Auto-discovered rules (#3)   | LOW                      |

## Validator checks

- `overrides "X"` must name a rule that exists. A typo surfaces as an
  error with a "did you mean…?" suggestion.
- You cannot override a strict rule. The validator rejects that
  configuration outright.

## Runtime

The resolver lives in `internal/defeasible`. Call
`defeasible.Resolve(matched)` with the list of rules that matched a
target; it returns the rules that should fire plus any tie warnings.
The package has no executor dependency, so it works as a building block
for any code that needs to combine rule outcomes.
