package factstore

import (
	"context"
	"errors"
	"time"
)

// TimeTraveler is an optional FactStore capability: evaluating a query
// against the store's state as it existed at a past instant.
//
// It is not part of the core FactStore interface — only backends that
// retain per-fact history (MemoryStore's append-only log, Datalevin's
// transaction history, talon-db's per-doc version chain) can answer it.
// Consumers type-assert for it and surface ErrNoTimeTravel when the
// configured backend can't:
//
//	if tt, ok := store.(factstore.TimeTraveler); ok {
//	    rows, err := tt.QueryAsOf(ctx, q, asOf)
//	}
//
// asOf is compared against fact write-times; QueryAsOf sees, for each
// cell, the value in effect at that instant (or none, if the cell was
// created later or retracted by then). It powers the `was <condition>
// N <unit> ago` detect condition.
type TimeTraveler interface {
	QueryAsOf(ctx context.Context, q Query, asOf time.Time) ([][]any, error)
}

// ErrNoTimeTravel is returned by the executor when a plan needs a
// time-travel query but the configured backend does not implement
// TimeTraveler.
var ErrNoTimeTravel = errors.New("factstore: backend does not support time-travel queries")
