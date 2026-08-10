# Time-travel detection — talon-db demo

An end-to-end demo of the `was ( … ) N <unit> ago` detect condition running
against a real **talon-db** backend. It flags machines that are **defective
now** but were **certified 90 days ago** — a regression an agent should
investigate.

`certification.tln` holds the program:

```talon
detect "Certification regressed" {
  for records where type == "machine"
    and status == "defective"
    and was (status == "certified") 30 days ago
  flag matching items
  ...
}
```

## Run it

```
$ go run ./examples/time_travel
detect "Certification regressed" flagged 2 machine(s) — defective now, certified 90 days ago:
  • Loader B (record 2)
  • Crane C (record 3)

not flagged: Excavator A (still certified), Forklift D (never certified)
```

## Why this is a Go program, not a REPL session

The other example agents run interactively from `talon repl`. Time-travel
can't: it needs facts written at **different points in time**, and talon-db
stamps its version history with the server clock — which isn't controllable
over the wire.

So this demo embeds the real talon-db stack in-process (bbolt store + gRPC
server + the FactStore adapter, talking over a Unix socket) and drives the
store clock to backdate the "90 days ago" writes. Everything past the socket
— history storage, the `QueryAsOf` RPC, the adapter — is the exact code that
runs in production; only the clock is under test control.

The detect then runs through `pkg/talon.Run` with the talon-db adapter as
its `FactStore`, exactly as a deployed agent would.

## See also

- [`docs/time_travel.md`](../../docs/time_travel.md) — the feature spec:
  syntax, semantics, the `TimeTraveler` capability, and backend support.
