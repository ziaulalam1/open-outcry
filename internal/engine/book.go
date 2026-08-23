package engine

import "github.com/ziaulalam1/open-outcry/internal/invariant"

// Book is the order book. Exactly one pointer to one of these exists in the
// running process, held by exactly one goroutine. Everything about the system's
// thread-safety follows from that sentence and from nothing in this file.
type Book struct {
	cfg   Config
	bids  ladder
	asks  ladder
	index map[OrderID]*Order

	seq    Seq
	nextID OrderID

	// led is folded from emitted events only. Note what is NOT here: a running
	// total of resting quantity. There deliberately is no cached aggregate for
	// the checker to read, so it has to walk the book and derive one itself.
	led invariant.Ledger

	lastTrade Traded
	hasTrade  bool
}

func New(cfg Config) *Book {
	if cfg.Depth <= 0 {
		cfg.Depth = 8
	}
	return &Book{
		cfg:   cfg,
		bids:  ladder{side: Buy},
		asks:  ladder{side: Sell},
		index: make(map[OrderID]*Order),
	}
}

// Seq returns the number of commands applied so far.
func (b *Book) Seq() Seq { return b.seq }

// Apply is the entire write surface of the domain.
//
// No context, no error, no channel, no interface{}. A command goes in, a value
// comes out, and the invariant report is a FIELD of that value rather than a
// separate call — so the check cannot be forgotten, skipped, or made
// conditional. There is no way to obtain a Result without one having run.
func (b *Book) Apply(c Command) Result {
	b.seq++

	// Photograph both frontiers BEFORE anything moves. This is the pre-image the
	// command is about to destroy; without it the checker cannot verify which
	// order should have been matched, because afterwards that order is gone.
	pre := [2]invariant.Frontier{
		Buy:  b.bids.frontier(),
		Sell: b.asks.frontier(),
	}

	var (
		events []Event
		fills  []invariant.Fill
		taker  invariant.Taker
	)
	switch cmd := c.(type) {
	case Submit:
		events, fills, taker = b.submit(cmd)
	case Cancel:
		events = b.cancel(cmd)
	}

	report := invariant.Check(invariant.Input{
		Book:   bookView{b},
		Ledger: b.led,
		Pre:    pre,
		Fills:  fills,
		Taker:  taker,
		Seq:    uint64(b.seq),
	})

	return Result{
		Seq:      b.seq,
		Events:   events,
		Snapshot: b.snapshot(),
		Report:   report,
	}
}

func (b *Book) submit(c Submit) ([]Event, []invariant.Fill, invariant.Taker) {
	// A rejected order never entered the book, so it is not recorded in the
	// ledger and is not a term in the conservation identity.
	if c.Qty <= 0 {
		return []Event{Rejected{Session: c.Session, Side: c.Side, Price: c.Price, Qty: c.Qty, Reason: RejectBadQuantity}}, nil, invariant.Taker{}
	}
	if c.Price <= 0 {
		return []Event{Rejected{Session: c.Session, Side: c.Side, Price: c.Price, Qty: c.Qty, Reason: RejectBadPrice}}, nil, invariant.Taker{}
	}

	b.nextID++
	o := &Order{
		ID:      b.nextID,
		Session: c.Session,
		Side:    c.Side,
		Price:   c.Price,
		Qty:     c.Qty,
		Open:    c.Qty,
		Seq:     b.seq,
	}
	b.led.Accept(uint8(c.Side), int64(c.Qty))

	events := make([]Event, 0, 4)
	events = append(events, Accepted{Order: *o}) // a value copy: the caller never gets our pointer

	fills, trades := b.match(o)
	events = append(events, trades...)

	if o.Open > 0 {
		b.own(o.Side).insert(o)
		b.index[o.ID] = o
		events = append(events, Rested{
			ID: o.ID, Session: o.Session, Side: o.Side,
			Price: o.Price, Open: o.Open, Seq: o.Seq,
		})
	}

	return events, fills, invariant.Taker{
		Present: true,
		Side:    uint8(c.Side),
		Price:   int64(c.Price),
		Left:    int64(o.Open),
	}
}

func (b *Book) cancel(c Cancel) []Event {
	o, ok := b.index[c.ID]
	if !ok {
		return []Event{Rejected{Session: c.Session, Reason: RejectUnknownOrder}}
	}
	// You may only cancel your own order. Sessions are anonymous, but they are
	// not a free-for-all: without this, one phone could clear the book.
	if o.Session != c.Session {
		return []Event{Rejected{Session: c.Session, Reason: RejectNotOwner}}
	}

	released := o.Open
	if !b.own(o.Side).removeOrder(o) {
		// The index and the ladder disagreed, which is a bug in this package
		// rather than bad input. Reject rather than corrupt: the conservation
		// check on the way out will not be fooled either way.
		return []Event{Rejected{Session: c.Session, Reason: RejectUnknownOrder}}
	}
	delete(b.index, o.ID)
	b.led.Cancel(uint8(o.Side), int64(released))

	return []Event{Canceled{
		ID: o.ID, Session: o.Session, Side: o.Side,
		Price: o.Price, Released: released,
	}}
}

func (b *Book) own(s Side) *ladder {
	if s == Buy {
		return &b.bids
	}
	return &b.asks
}

func (b *Book) opposite(s Side) *ladder { return b.own(s.Opposite()) }
