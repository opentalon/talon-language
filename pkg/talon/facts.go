package talon

import "github.com/opentalon/talon-language/internal/factstore"

// Fact is a single EAV triple written to a FactStore. RecordID +
// Attribute identify the cell; Value carries the data. Entity holds the
// tenant ID (reserved for the multi-tenant work in #15). Aliased from
// internal/factstore so external callers can build facts for
// [FactStore.Assert] without importing an internal package.
type Fact = factstore.Fact

// Event is a single mutation observed by a FactStore, delivered to
// subscribers. Kind reports why it fired; Fact carries the new (or
// removed) triple, and Prev carries the previous value on a change.
type Event = factstore.Event

// EventKind identifies why an [Event] was emitted: [EventAssert],
// [EventRetract], or [EventChange].
type EventKind = factstore.EventKind

// RetractPattern selects facts to remove via [FactStore.Retract]. An
// empty Attribute retracts the whole entity; a nil Value retracts every
// value of the attribute.
type RetractPattern = factstore.RetractPattern

// FactStore event kinds.
const (
	// EventAssert fires after a new fact is added to the store.
	EventAssert = factstore.EventAssert
	// EventRetract fires before a fact is removed.
	EventRetract = factstore.EventRetract
	// EventChange fires when an existing attribute's value changes;
	// the event's Prev holds the previous value.
	EventChange = factstore.EventChange
)
