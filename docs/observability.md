# Observability

This page describes the structured-logging foundation that ships in this
release. Tracing CLI, metrics endpoint, and the audit log are tracked
under [#20](https://github.com/opentalon/tln-language/issues/20) and
land in follow-up PRs.

## What gets logged today

The runtime emits structured records on three occasions:

| Event       | When it fires                                | Level | Key attributes |
| ----------- | -------------------------------------------- | ----- | --- |
| `block_eval` | A block (detect / rule / forecast / …) finishes evaluation | INFO  | `rule`, `type`, `matched`, `duration_ms` |
| `mcp_call`   | The executor calls an MCP tool                | INFO / ERROR | `plugin`, `action`, `status`, `duration_ms`, `error?` |
| (custom)     | An `on { logger.info "…" }` statement fires   | INFO / WARN / ERROR | `source=on_block`, `trigger`, `block`, message |

All output goes to **stderr** so it can never collide with the
`stdout`-bound payload of `tln run`, `tln explain`, or `tln repl`.

## CLI flags

Two global flags configure logging. They come *before* the subcommand:

```
tln [--log-format=text|json] [--log-level=debug|info|warn|error] <command> [args]
```

| Flag | Default | Values |
| --- | --- | --- |
| `--log-format` | `text` | `text` (key=value, slog's `TextHandler`), `json` (one JSON object per line) |
| `--log-level` | `warn`  | `debug`, `info`, `warn`, `error` |

Defaults are intentionally quiet — `tln build` and `tln repl` produce
no log noise out of the box. Opt into operational visibility with
`--log-level=info`.

## Examples

### Pipe runtime events to a log aggregator

```bash
tln --log-format=json --log-level=info run rules.tln --seed fixtures.tln.test 2>events.jsonl
jq '.' events.jsonl
```

Each line decodes as:

```json
{
  "time": "2026-06-08T15:30:00Z",
  "level": "INFO",
  "msg": "block_eval",
  "rule": "Service overdue",
  "type": "detect",
  "matched": 3,
  "duration_ms": 12
}
```

### Watch evaluations from the REPL

```bash
tln --log-level=info repl
tln> :load examples/cement_explain.tln
tln> :load test/cement_explain.tln.test
tln> :eval "Cement running low"
time=… level=INFO msg=block_eval rule="Cement running low" type=detect matched=1 duration_ms=0
  "Cement running low": 1 detection(s) — records [808]
```

The log line precedes the `:eval` output so you can watch both in
real time.

### Logger statements inside any block

Per-row logger statements work inside `detect`, `rule`, and
`recommend` bodies — usually the most useful shape for "this rule
fired for which row":

```tln
detect "Service overdue" {
  for records where type == "item"
    and attr "km" > attr "last_service_km" + 20000
  flag matching items
  label "{item.name}: overdue"
  logger.info "fired for {item.name}: {attr.km} km"
}
```

The template renders against the matched row the same way
`label` / `reason` / `suggest` do — `{item.name}`, `{attr.x}`,
`{context.role}`, and the template functions from #60 (`{count}`,
`{avg(attr.x)}`, …) all work. One log record per flagged entity,
emitted at the declared level (`info` / `warn` / `error`) through
`internal/log`.

Each record carries `source=block_logger`, `block=<name>`,
`entity_id=<id>` so downstream aggregators can pivot on rule firings.

### Logger statements inside `on { }` blocks

The same `logger.info|warn|error "…"` statement also works inside
reactive `on { }` bodies. Wire up a dispatcher with
`reactive.LoggingActionHandler()`:

```go
d := reactive.New(reactive.LoggingActionHandler())
for _, b := range program.Blocks {
    if on, ok := b.(*ast.OnBlock); ok {
        d.Register(on)
    }
}
d.Subscribe(&store.Events)
```

When the FactStore emits a change/assert/retract event that matches a
registered `on` block, its body's `logger.<level>` statements run
through `internal/log`, picking up the same `--log-format` /
`--log-level` settings the CLI was started with. Message templates
support a small set of event-scoped placeholders:

| Placeholder        | Substituted with |
| ------------------ | --- |
| `{event.attr}`     | The attribute name on the triggering fact |
| `{event.value}`    | New value |
| `{event.prev}`     | Previous value (on `change` events) |
| `{event.entity}`   | Entity ID |

Example:

```tln
on change attr "current_stock" {
  logger.warn "stock changed for {event.entity}: {event.prev} -> {event.value}"
}
```

emits:

```json
{"level":"WARN","msg":"stock changed for 501: 20 -> 10","source":"on_block","trigger":"change","block":"on change attr \"current_stock\""}
```

## What's coming next

The structured logger is the foundation for the rest of the
observability RFC:

- **Audit log** — a sink that subscribes to `rule_action` records and
  appends them to a durable store.
- **Prometheus metrics** — counters and histograms wired to the same
  event stream behind an optional `/metrics` HTTP endpoint.
- **`tln trace --entity N`, `tln status`, `tln audit`** — CLI
  surfaces that consume the audit and metrics stores.

See [#20](https://github.com/opentalon/tln-language/issues/20) for the
full plan.
