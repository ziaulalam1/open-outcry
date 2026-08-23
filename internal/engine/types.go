// Package engine is the matching engine. It knows nothing about transport.
//
// It has never heard of JSON, HTTP or WebSocket, has no clock, no goroutines and
// no channels, and its only import outside the standard library is the invariant
// checker. Its entire write surface is one method:
//
//	func (b *Book) Apply(c Command) Result
//
// Commands in, values out. Every concurrency guarantee in the running system is
// therefore a property of the one goroutine that owns the one *Book pointer, not
// of anything in this package — which is why "no data races by construction" is
// a claim about ownership rather than about locks. There are no locks here.
package engine

import "github.com/ziaulalam1/open-outcry/internal/invariant"

type Side uint8

const (
	Buy  Side = 0
	Sell Side = 1
)

func (s Side) String() string {
	if s == Buy {
		return "buy"
	}
	return "sell"
}

func (s Side) opposite() Side { return 1 - s }

// Ticks is a price in CENTS. There is no float anywhere in this repository.
//
// Floats drift, and a drifting price makes the conservation identity
// undecidable: you can only ever assert two notionals are "close", which is not
// a proof of anything. Integer ticks are what let the checker use ==.
type Ticks int64

// Qty is a whole number of shares.
type Qty int64

type OrderID uint64

// Seq is the engine's ONLY notion of time: a monotonic arrival ordinal.
//
// No wall clock appears in this package. That is deliberate and it buys three
// things: replay is exact, fuzzing is deterministic, and price-time priority
// becomes a statement about integers rather than about timestamps that can tie.
type Seq uint64

// SessionID is opaque. The engine never interprets it — it carries it on events
// so the transport can route fills back to a phone, and that is all.
type SessionID string

type Order struct {
	ID      OrderID
	Session SessionID
	Side    Side
	Price   Ticks
	Qty     Qty // as submitted
	Open    Qty // unfilled remainder
	Seq     Seq // arrival ordinal — the "time" in price-time priority
}

type RejectReason uint8

const (
	RejectNone RejectReason = iota
	RejectBadQuantity
	RejectBadPrice
	RejectUnknownOrder
	RejectNotOwner
)

func (r RejectReason) String() string {
	switch r {
	case RejectBadQuantity:
		return "BAD_QUANTITY"
	case RejectBadPrice:
		return "BAD_PRICE"
	case RejectUnknownOrder:
		return "UNKNOWN_ORDER"
	case RejectNotOwner:
		return "NOT_OWNER"
	}
	return "NONE"
}

// Config is the whole of the engine's configuration. Depth affects only what a
// Snapshot carries; it has no effect on matching.
type Config struct {
	Depth int // price levels per side in a Snapshot; <= 0 means 8
}

// Result is everything one command produced.
type Result struct {
	Seq      Seq
	Events   []Event // in emission order
	Snapshot Snapshot
	Report   invariant.Report
}

// OK reports whether every invariant held. It is a method on Result rather than
// something the caller assembles, because the check is not optional: there is no
// way to obtain a Result without one having run.
func (r Result) OK() bool { return r.Report.OK() }
