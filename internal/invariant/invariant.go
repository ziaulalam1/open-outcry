// Package invariant checks that an order book is internally consistent, using
// only what it can see through the read-only interfaces declared here.
//
// It imports nothing from this module, and that is the entire point.
//
// The checker must not be able to read a number computed by the code it audits.
// If conservation were checked against a running "total resting" counter that
// the matcher maintains, then a wrong decrement would move both sides of the
// equation together and the check would pass vacuously — staying green through
// exactly the class of bug it exists to catch. Across this package boundary the
// checker cannot name engine.Book, cannot reach an unexported field, and cannot
// be handed a convenient precomputed answer. It re-derives what it needs by
// walking the book, and compares that against a ledger folded from the events
// the engine said it emitted.
//
// Agreement between two independently produced representations is evidence.
// Agreement between a value and itself is not.
package invariant

import (
	"fmt"
	"strings"
)

// Sides. The checker uses raw uint8 rather than importing a domain type,
// because importing a domain type is the thing it is forbidden to do.
const (
	Buy  uint8 = 0
	Sell uint8 = 1
)

// OrderRef is everything the checker is permitted to know about a resting order.
type OrderRef struct {
	Seq  uint64 // arrival ordinal — the "time" in price-time priority
	Open int64  // unfilled quantity
}

// Level is one price level, its orders in FIFO order.
type Level interface {
	Price() int64
	Len() int
	At(i int) OrderRef // i == 0 is the front of the queue
}

// Ladder is one side of the book, best price first.
type Ladder interface {
	Len() int
	Level(i int) Level // i == 0 is the best price
}

// Book is the read-only window the checker gets. Note what is absent: there is
// no way to ask the book for a total, a count, or any aggregate. Every quantity
// the checker uses, it adds up itself.
type Book interface {
	Bids() Ladder
	Asks() Ladder
}

// Ledger is folded exclusively from emitted events. It never touches the book.
//
// This is the second of the two representations. The engine tells the ledger
// what it *did*; the walk measures what it *holds*. Disagreement is the signal.
type Ledger struct {
	Submitted [2]int64 // accepted quantity, by side
	Filled    [2]int64
	Canceled  [2]int64
	Notional  [2]int64 // sum(price_cents * qty)
}

// Accept records quantity the engine admitted to the book. Rejected quantity
// never entered, so it is an observability counter, not a term in the identity.
func (l *Ledger) Accept(side uint8, qty int64) { l.Submitted[side] += qty }

// Fill records a trade. A trade is a transfer, so it credits BOTH sides: that
// is what makes "no shares created or destroyed" a statement rather than a slogan.
func (l *Ledger) Fill(taker uint8, qty, price int64) {
	l.Filled[taker] += qty
	l.Filled[1-taker] += qty
	l.Notional[taker] += price * qty
	l.Notional[1-taker] += price * qty
}

func (l *Ledger) Cancel(side uint8, qty int64) { l.Canceled[side] += qty }

// Frontier is an O(1) photograph of one side's best level, taken BEFORE the
// command ran. It is the pre-image the post-state destroyed, and without it
// price-time priority cannot be checked as a post-condition at all: the order
// that was matched is, by then, gone.
type Frontier struct {
	Empty    bool
	Price    int64
	HeadSeq  uint64
	HeadOpen int64
}

// Fill is one trade, as the engine reported it. The checker treats this list as
// an independent witness — it never reads the matcher's local variables.
type Fill struct {
	Price     int64
	Qty       int64
	MakerSeq  uint64
	MakerLeft int64 // maker's open quantity AFTER this fill, per the event
}

// Taker describes the aggressing order, if the command had one.
type Taker struct {
	Present bool
	Side    uint8
	Price   int64
	Left    int64 // remainder after the sweep (rested, or zero)
}

type Kind uint8

const (
	Conservation Kind = iota
	NoCrossedBook
	PriceTimePriority
	SweepComplete
	numKinds
)

func (k Kind) String() string {
	switch k {
	case Conservation:
		return "CONSERVATION"
	case NoCrossedBook:
		return "NO_CROSSED_BOOK"
	case PriceTimePriority:
		return "PRICE_TIME_PRIORITY"
	case SweepComplete:
		return "SWEEP_COMPLETE"
	}
	return "UNKNOWN"
}

type Violation struct {
	Kind   Kind
	Detail string
	Want   int64
	Got    int64
}

func (v Violation) String() string {
	return fmt.Sprintf("%s: %s (want %d, got %d)", v.Kind, v.Detail, v.Want, v.Got)
}

type Report struct {
	Seq        uint64
	Passed     [numKinds]bool
	Violations []Violation
}

func (r Report) OK() bool { return len(r.Violations) == 0 }

func (r Report) String() string {
	if r.OK() {
		return fmt.Sprintf("seq %d: all checks passed", r.Seq)
	}
	parts := make([]string, 0, len(r.Violations))
	for _, v := range r.Violations {
		parts = append(parts, v.String())
	}
	return fmt.Sprintf("seq %d: %s", r.Seq, strings.Join(parts, "; "))
}

// Input is what the engine hands the checker after every command.
type Input struct {
	Book   Book
	Ledger Ledger
	Pre    [2]Frontier // frontiers captured before the command ran
	Fills  []Fill      // trades emitted by this command, in execution order
	Taker  Taker
	Seq    uint64
}

// Check runs every invariant. It is a pure function: no clock, no state, no I/O,
// no goroutines. That is what lets the hot-path check and the test oracle be the
// same code — the tests do not re-implement the assertions, they call this.
func Check(in Input) Report {
	r := Report{Seq: in.Seq}
	for i := range r.Passed {
		r.Passed[i] = true
	}
	add := func(k Kind, detail string, want, got int64) {
		r.Passed[k] = false
		r.Violations = append(r.Violations, Violation{Kind: k, Detail: detail, Want: want, Got: got})
	}

	w := walk(in.Book, len(in.Fills) > 0, add)

	checkConservation(in, w, add)
	checkCrossed(w, add)
	checkPriority(in, w, add)
	checkSweep(in, w, add)

	return r
}

// walked is the result of the single fused pass over the book.
type walked struct {
	resting [2]int64
	best    [2]int64
	has     [2]bool
	seqOpen map[uint64]int64 // built only when there are fills to cross-check
}

// walk re-derives every aggregate the checker needs, and validates structure on
// the way through. One pass, O(orders + levels).
func walk(b Book, wantSeqOpen bool, add func(Kind, string, int64, int64)) walked {
	w := walked{}
	if wantSeqOpen {
		w.seqOpen = make(map[uint64]int64, 16)
	}
	w.resting[Buy], w.best[Buy], w.has[Buy] = walkSide(b.Bids(), Buy, w.seqOpen, add)
	w.resting[Sell], w.best[Sell], w.has[Sell] = walkSide(b.Asks(), Sell, w.seqOpen, add)
	return w
}

func walkSide(lad Ladder, side uint8, seqOpen map[uint64]int64, add func(Kind, string, int64, int64)) (total, best int64, has bool) {
	name := "bids"
	if side == Sell {
		name = "asks"
	}
	var prevPrice int64
	for i := 0; i < lad.Len(); i++ {
		lv := lad.Level(i)
		price := lv.Price()

		if lv.Len() == 0 {
			// An empty level is not merely untidy: it means a level outlived its
			// last order, and the next order at that price would silently inherit
			// a queue position it never earned.
			add(PriceTimePriority, name+": empty price level "+itoa(price), 0, 0)
		}
		if i > 0 {
			// Bids descend from the best, asks ascend. Equality is a violation
			// too: two levels at one price means the FIFO is split in half.
			bad := (side == Buy && price >= prevPrice) || (side == Sell && price <= prevPrice)
			if bad {
				add(PriceTimePriority, name+": levels out of order", prevPrice, price)
			}
		}
		prevPrice = price

		var prevSeq uint64
		for j := 0; j < lv.Len(); j++ {
			o := lv.At(j)
			if o.Open <= 0 {
				add(PriceTimePriority, name+": non-positive resting quantity at "+itoa(price), 1, o.Open)
			}
			if j > 0 && o.Seq <= prevSeq {
				// Strictly increasing: within a level, earlier arrival must be
				// nearer the front. This is the "time" half of price-time priority
				// expressed as a property of the book itself.
				add(PriceTimePriority, name+": queue out of arrival order at "+itoa(price),
					int64(prevSeq), int64(o.Seq))
			}
			prevSeq = o.Seq
			total += o.Open
			if seqOpen != nil {
				seqOpen[o.Seq] = o.Open
			}
		}
		if i == 0 {
			best, has = price, true
		}
	}
	return total, best, has
}

func itoa(v int64) string { return fmt.Sprintf("%d", v) }
