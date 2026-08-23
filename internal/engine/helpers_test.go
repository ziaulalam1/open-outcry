package engine

import "testing"

// apply runs a command and FAILS THE TEST if any invariant broke.
//
// This is the single most important line in the test suite: the assertions that
// run in the hot path on stage are the same code that grades every test here.
// There is no second implementation of "is the book correct" to drift out of
// sync with the first. A test that does not use this helper is asserting
// something about events or shape; every test that does use it is also, for
// free, asserting conservation, no-crossed-book, price-time priority, sweep
// completeness and maker-residual arithmetic.
func apply(t *testing.T, b *Book, c Command) Result {
	t.Helper()
	r := b.Apply(c)
	if !r.OK() {
		t.Fatalf("invariant violated applying %#v\n  %s", c, r.Report)
	}
	return r
}

func newBook() *Book { return New(Config{Depth: 8}) }

func buy(price, qty int64) Submit {
	return Submit{Session: "s1", Side: Buy, Price: Ticks(price), Qty: Qty(qty)}
}

func sell(price, qty int64) Submit {
	return Submit{Session: "s2", Side: Sell, Price: Ticks(price), Qty: Qty(qty)}
}

func buyAs(sess string, price, qty int64) Submit {
	return Submit{Session: SessionID(sess), Side: Buy, Price: Ticks(price), Qty: Qty(qty)}
}

func sellAs(sess string, price, qty int64) Submit {
	return Submit{Session: SessionID(sess), Side: Sell, Price: Ticks(price), Qty: Qty(qty)}
}

func trades(r Result) []Traded {
	var out []Traded
	for _, e := range r.Events {
		if t, ok := e.(Traded); ok {
			out = append(out, t)
		}
	}
	return out
}

func rested(r Result) (Rested, bool) {
	for _, e := range r.Events {
		if x, ok := e.(Rested); ok {
			return x, true
		}
	}
	return Rested{}, false
}

func rejected(r Result) (Rejected, bool) {
	for _, e := range r.Events {
		if x, ok := e.(Rejected); ok {
			return x, true
		}
	}
	return Rejected{}, false
}

// restingID finds the id of the order that rested from this result.
func restingID(t *testing.T, r Result) OrderID {
	t.Helper()
	x, ok := rested(r)
	if !ok {
		t.Fatalf("expected an order to rest, got events %+v", r.Events)
	}
	return x.ID
}
