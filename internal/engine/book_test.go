package engine

import "testing"

func TestSubmitRestsWhenNothingToTrade(t *testing.T) {
	b := newBook()
	r := apply(t, b, buy(10240, 300))

	if len(trades(r)) != 0 {
		t.Fatalf("empty book should produce no trades, got %d", len(trades(r)))
	}
	x, ok := rested(r)
	if !ok {
		t.Fatal("order should have rested")
	}
	if x.Open != 300 || x.Price != 10240 {
		t.Fatalf("rested %d @ %d, want 300 @ 10240", x.Open, x.Price)
	}
	if got := r.Snapshot.Resting[Buy]; got != 300 {
		t.Fatalf("resting buy = %d, want 300", got)
	}
}

func TestAcceptedIsEmittedBeforeAnyFill(t *testing.T) {
	b := newBook()
	apply(t, b, sell(10250, 100))
	r := apply(t, b, buy(10250, 100))

	if _, ok := r.Events[0].(Accepted); !ok {
		// Replaying the event stream has to rebuild the same book. That only
		// works if the order exists before it trades.
		t.Fatalf("first event = %T, want Accepted", r.Events[0])
	}
}

func TestRejectBadQuantityChangesNothing(t *testing.T) {
	b := newBook()
	apply(t, b, buy(10240, 300))
	before := b.Apply(buy(10245, 100)).Snapshot

	for _, bad := range []Submit{
		{Session: "s1", Side: Buy, Price: 10240, Qty: 0},
		{Session: "s1", Side: Buy, Price: 10240, Qty: -50},
	} {
		r := apply(t, b, bad)
		x, ok := rejected(r)
		if !ok || x.Reason != RejectBadQuantity {
			t.Fatalf("qty %d: got %+v, want BAD_QUANTITY", bad.Qty, r.Events)
		}
		if len(trades(r)) != 0 {
			t.Fatal("a rejected order must not trade")
		}
	}

	after := b.Apply(Cancel{Session: "nobody", ID: 9999}).Snapshot
	if after.Resting != before.Resting || after.Submitted != before.Submitted {
		t.Fatalf("rejects changed book state: before %v/%v after %v/%v",
			before.Resting, before.Submitted, after.Resting, after.Submitted)
	}
}

func TestRejectBadPrice(t *testing.T) {
	b := newBook()
	r := apply(t, b, Submit{Session: "s1", Side: Buy, Price: 0, Qty: 100})
	x, ok := rejected(r)
	if !ok || x.Reason != RejectBadPrice {
		t.Fatalf("got %+v, want BAD_PRICE", r.Events)
	}
}

// Rejected quantity never entered the book, so it must not appear in the
// conservation identity. If it were counted as submitted, the identity would
// fail immediately — which is a good way to notice you got this wrong.
func TestRejectedQuantityIsNotSubmitted(t *testing.T) {
	b := newBook()
	apply(t, b, buy(10240, 300))
	apply(t, b, Submit{Session: "s1", Side: Buy, Price: 10240, Qty: 0})
	r := apply(t, b, Submit{Session: "s1", Side: Buy, Price: -5, Qty: 100})

	if got := r.Snapshot.Submitted[Buy]; got != 300 {
		t.Fatalf("submitted = %d, want 300 (rejects excluded)", got)
	}
}

func TestCancelRemovesRestingOrder(t *testing.T) {
	b := newBook()
	r := apply(t, b, buy(10240, 300))
	id := restingID(t, r)

	r = apply(t, b, Cancel{Session: "s1", ID: id})

	var c Canceled
	found := false
	for _, e := range r.Events {
		if x, ok := e.(Canceled); ok {
			c, found = x, true
		}
	}
	if !found {
		t.Fatalf("no Canceled event, got %+v", r.Events)
	}
	if c.Released != 300 {
		t.Fatalf("released %d, want 300", c.Released)
	}
	if got := r.Snapshot.Resting[Buy]; got != 0 {
		t.Fatalf("resting = %d, want 0", got)
	}
	if len(r.Snapshot.Bids) != 0 {
		t.Fatalf("drained level should be gone, got %d levels", len(r.Snapshot.Bids))
	}
}

func TestCancelUnknownOrder(t *testing.T) {
	b := newBook()
	r := apply(t, b, Cancel{Session: "s1", ID: 12345})
	x, ok := rejected(r)
	if !ok || x.Reason != RejectUnknownOrder {
		t.Fatalf("got %+v, want UNKNOWN_ORDER", r.Events)
	}
}

// Sessions are anonymous, but they are not a free-for-all: without an owner
// check one phone could clear the whole book.
func TestCancelRequiresOwnership(t *testing.T) {
	b := newBook()
	id := restingID(t, apply(t, b, buyAs("alice", 10240, 300)))

	r := apply(t, b, Cancel{Session: "mallory", ID: id})
	x, ok := rejected(r)
	if !ok || x.Reason != RejectNotOwner {
		t.Fatalf("got %+v, want NOT_OWNER", r.Events)
	}
	if got := r.Snapshot.Resting[Buy]; got != 300 {
		t.Fatalf("resting = %d, want 300 — the order must survive", got)
	}
}

func TestCancelOfPartiallyFilledOrderReleasesOnlyTheRemainder(t *testing.T) {
	b := newBook()
	id := restingID(t, apply(t, b, buyAs("alice", 10240, 300)))
	apply(t, b, sellAs("bob", 10240, 120)) // fills 120 of alice's 300

	r := apply(t, b, Cancel{Session: "alice", ID: id})
	for _, e := range r.Events {
		if c, ok := e.(Canceled); ok && c.Released != 180 {
			t.Fatalf("released %d, want 180", c.Released)
		}
	}
	if got := r.Snapshot.Filled[Buy]; got != 120 {
		t.Fatalf("filled = %d, want 120", got)
	}
}

func TestSnapshotDepthIsBounded(t *testing.T) {
	b := New(Config{Depth: 3})
	for i := 0; i < 8; i++ {
		apply(t, b, buy(int64(10240-i*5), 100))
	}
	if len(b.Apply(Cancel{ID: 0}).Snapshot.Bids) != 3 {
		t.Fatalf("depth not honoured")
	}
}

// The aliasing rule, asserted rather than trusted: a snapshot handed to another
// goroutine must not share memory with the live book. Single-goroutine tests run
// under -race will never catch a violation of this, so it needs its own test.
func TestSnapshotSharesNoMemoryWithTheBook(t *testing.T) {
	b := newBook()
	apply(t, b, buy(10240, 300))
	apply(t, b, sell(10260, 400))
	r := apply(t, b, buy(10245, 100))
	snap := r.Snapshot

	bidsBefore := append([]Level(nil), snap.Bids...)
	asksBefore := append([]Level(nil), snap.Asks...)
	lastBefore := snap.LastTrade

	// Move the book substantially underneath the snapshot we are holding.
	for i := 0; i < 20; i++ {
		apply(t, b, sell(10240, 50))
		apply(t, b, buy(10260, 50))
	}

	for i := range bidsBefore {
		if snap.Bids[i] != bidsBefore[i] {
			t.Fatalf("snapshot bid level %d mutated: %+v -> %+v", i, bidsBefore[i], snap.Bids[i])
		}
	}
	for i := range asksBefore {
		if snap.Asks[i] != asksBefore[i] {
			t.Fatalf("snapshot ask level %d mutated: %+v -> %+v", i, asksBefore[i], snap.Asks[i])
		}
	}
	if snap.LastTrade != lastBefore {
		t.Fatalf("snapshot LastTrade mutated: %+v -> %+v", lastBefore, snap.LastTrade)
	}
}
