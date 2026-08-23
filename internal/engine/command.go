package engine

// Command is a sealed union. The unexported marker method means no package
// outside this one can declare a Command, so no adapter can teach the domain a
// transport concept by smuggling in its own case. Decoding JSON into one of
// these is the transport's job and happens at the edge; by the time a value
// reaches Apply it is already a domain value.
type Command interface{ isCommand() }

// Submit is a LIMIT order: match at the limit or better, rest the remainder.
//
// LIMIT is the only order type, and that is a scoping decision rather than an
// omission. The phone UI collects side, price and quantity, which IS a limit
// order; MARKET, IOC and FOK would add reject paths and a second UI mode without
// adding anything to the concurrency story this project exists to tell.
type Submit struct {
	Session SessionID
	Side    Side
	Price   Ticks
	Qty     Qty
}

// Cancel removes the caller's resting order.
//
// Not surfaced in the phone UI for v1, but present and fully tested here: the
// conservation identity is uninteresting without a term that can leave the book
// without trading, and cancel is that term.
type Cancel struct {
	Session SessionID
	ID      OrderID
}

func (Submit) isCommand() {}
func (Cancel) isCommand() {}
