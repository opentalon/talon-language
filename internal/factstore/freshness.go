package factstore

import "time"

// Freshness is an optional FactStore capability: the wall-clock time a
// fact — identified by record ID and attribute — was last asserted.
//
// It is not part of the core FactStore interface (many backends can't
// answer it cheaply). Consumers type-assert for it and treat a store that
// doesn't implement it, or an (_, false) result, as "freshness unknown":
//
//	if fs, ok := store.(factstore.Freshness); ok {
//	    if t, known := fs.LastWritten(id, attr); known { ... }
//	}
//
// "Last asserted" is deliberately independent of whether the value
// changed: re-asserting the same value still records a refresh at that
// time, which is what staleness checks (e.g. the enrich block) need.
type Freshness interface {
	LastWritten(recordID, attribute string) (time.Time, bool)
}
