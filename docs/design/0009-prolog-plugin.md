# ADR 0009 — Prolog as an external reasoner plugin

Status: accepted

## Context

tln's core is a deterministic, **flat EAV** expert-system language. Its
well-founded resolver yields a single three-valued model
([docs/well-founded.md](../well-founded.md)), and its term type is only
`Var | Lit` — there are **no compound terms**. That is the right shape for one
auditable answer over records.

**Prolog** is the natural "logic programming" front-end, but its world is
**structured**: functor terms and lists, unification with occurs-check, and SLD
resolution with backtracking. None of that fits core:

- **Structured terms** — `factstore.Term` is `Var | Lit`; representing
  `path([a,b,c])` or `[H|T]` needs a richer IR core does not have.
- **SLD resolution** — depth-first proof search with backtracking and cut is a
  different evaluation model from the inline query planner / polynomial
  well-founded resolver.
- **`.pl` syntax** — plugins consume SPIs; they cannot extend core's
  lexer/parser. But Prolog source is `.pl`, read by the plugin into the plugin's
  own IR, so tln's `.tln` grammar never enters the picture. (Native tln
  term-syntax would be a separate "make tln itself a richer logic language"
  decision — orthogonal, and out of scope.)

## Decision

Prolog is an **external plugin** that **brings its own engine** — the fourth
plugin shape after tln-db (a *store*, `pkg/factstore.FactStore`), tln-mcp (a
*tool*, `tln.ToolResolver`), and tln-asp (a *solver*). tln-prolog is a
**reasoner**.

- **Repo:** `opentalon/tln-prolog`, pure Go, depends one-way on tln-language.
- **Its own IR** — `Var | Atom | Int | Compound`, lists as `"."/2` cells. The
  richer representation lives in the plugin, so it does **not** need
  `factstore.Rule`/`Term`. This makes it more independent from core, not less.
- **Its own unifier** — most-general unification with an always-on occurs-check.
- **Its own resolver** — a pure-Go SLD `Machine`: depth-first resolution,
  chronological backtracking, fresh clause renaming, and a depth bound that
  surfaces left recursion as `ErrDepthExceeded` instead of a hang. No cgo, no
  external Prolog.
- **Its own reader** — an ISO-subset `.pl` parser that records what it will not
  execute (cut, IO, `assert`/`retract`, arithmetic, `findall`, …) as typed
  `Diagnostic`s rather than dropping them silently.
- **Output boundary** — `AtomFacts` projects ground atoms to `[]factstore.Fact`
  (namespace `:pl/`), so answers feed any FactStore. Hosts wanting full term
  structure use the plugin's `Term`/`Solution` types directly.

Core stays flat, deterministic, and code-free; the plugin owns terms,
unification, and the search.

## Core change

**None.** Unlike ADR 0008 (which completed the SPI with `Negation`), this plugin
needs no core change: it uses only the already-public `pkg/factstore` `Fact`
type at its output boundary, and its entire term/unify/resolve/read stack is
plugin-side. Zero-core-change is the whole point — the earlier "gaps" were only
core changes if core's own resolver had to understand Prolog data. It does not;
the plugin *is* the engine.

## The store boundary is lossy by choice

Core is flat EAV; the plugin's world is structured terms. A compound-term answer
like `path([a,b,c])` has no faithful scalar form, so `AtomFacts` reifies it to
its canonical string and emits a `Diagnostic`. A host that wants full structure
uses the plugin's rich result type; one that just wants answers-as-facts gets the
projection — exactly the trade-off tln-asp makes at atoms→facts.

## Consequences / next steps

Separate repos, each importing this engine (not part of this ADR):

- **prolog2tln** — a transpiler that maps the runnable `.pl` subset to tln
  (`factstore.Rule` + `Fact`) and uses the reader's diagnostics to report what a
  deterministic EAV target cannot carry.
- **A Prolog CLI** — a command-line plugin that transforms a Prolog system into
  tln, built on prolog2tln.
- Engine-side roadmap: tabling for terminating left recursion, and an optional
  arithmetic evaluator behind a flag.

None of these change this boundary: the engine and the missing Prolog-language
parts live in tln-prolog; the transpiler and CLI are downstream plugins.
