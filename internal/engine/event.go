package engine

// Event is a sealed union, for the same reason Command is.
//
// Events are the engine's only output besides the snapshot, and they are the
// left-hand side of the conservation identity: the ledger is folded from these
// and from nothing else. That is what makes the identity a comparison of two
// independent representations rather than a tautology.
type Event interface{ isEvent() }

// Accepted means the order was admitted. It is emitted before any fill, so a
// replay of the event stream reconstructs the same book in the same order.
type Accepted struct{ Order Order }

// Rejected means nothing changed. Rejected quantity never entered the book, so
// it is deliberately NOT a term in the conservation identity.
type Rejected struct {
	Session SessionID
	Side    Side
	Price   Ticks
	Qty     Qty
	Reason  RejectReason
}

// Traded is one fill.
//
// MakerLeft is the maker's open quantity after this fill, and it is not
// redundant bookkeeping: it is a second, independently derived fact that lets
// the checker catch a matcher which used one wrong quantity for both the book
// decrement and the trade size. See invariant.checkMakerResidual.
//
// Price is always the PASSIVE order's price. A taker that crosses gets price
// improvement; it never pays its own limit when the resting side is better.
type Traded struct {
	Price     Ticks
	Qty       Qty
	TakerID   OrderID
	MakerID   OrderID
	TakerSeq  Seq
	MakerSeq  Seq
	Taker     SessionID
	Maker     SessionID
	TakerSide Side
	MakerLeft Qty
}

// Rested means the unfilled remainder joined the book at the back of its queue.
type Rested struct {
	ID      OrderID
	Session SessionID
	Side    Side
	Price   Ticks
	Open    Qty
	Seq     Seq
}

// Canceled means quantity left the book without trading.
type Canceled struct {
	ID       OrderID
	Session  SessionID
	Side     Side
	Price    Ticks
	Released Qty
}

func (Accepted) isEvent() {}
func (Rejected) isEvent() {}
func (Traded) isEvent()   {}
func (Rested) isEvent()   {}
func (Canceled) isEvent() {}
