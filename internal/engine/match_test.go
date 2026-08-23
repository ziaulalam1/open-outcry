package engine

import "testing"

func TestExactFillLeavesNothingResting(t *testing.T) {
	b := newBook()
	apply(t, b, sellAs("maker", 10250, 200))
	r := apply(t, b, buyAs("taker", 10250, 200))

	tr := trades(r)
	if len(tr) != 1 {
		t.Fatalf("got %d trades, want 1", len(tr))
	}
	if tr[0].Qty != 200 || tr[0].Price != 10250 {
		t.Fatalf("trade %d @ %d, want 200 @ 10250", tr[0].Qty, tr[0].Price)
	}
	if tr[0].MakerLeft != 0 {
		t.Fatalf("MakerLeft = %d, want 0", tr[0].MakerLeft)
	}
	if _, ok := rested(r); ok {
		t.Fatal("nothing should rest after an exact fill")
	}
	if r.Snapshot.Resting[Buy] != 0 || r.Snapshot.Resting[Sell] != 0 {
		t.Fatalf("book should be empty, got %v", r.Snapshot.Resting)
	}
}

func TestPartialFillTakerRemainderRests(t *testing.T) {
	b := newBook()
	apply(t, b, sellAs("maker", 10250, 80))
	r := apply(t, b, buyAs("taker", 10250, 200))

	tr := trades(r)
	if len(tr) != 1 || tr[0].Qty != 80 {
		t.Fatalf("got %+v, want one trade of 80", tr)
	}
	x, ok := rested(r)
	if !ok || x.Open != 120 {
		t.Fatalf("rested %+v, want remainder 120", x)
	}
	if r.Snapshot.Resting[Sell] != 0 {
		t.Fatal("maker side should be fully consumed")
	}
}

func TestPartialFillMakerRemainderStaysAtFront(t *testing.T) {
	b := newBook()
	apply(t, b, sellAs("maker", 10250, 500))
	r := apply(t, b, buyAs("taker", 10250, 120))

	tr := trades(r)
	if len(tr) != 1 || tr[0].Qty != 120 {
		t.Fatalf("got %+v, want one trade of 120", tr)
	}
	// MakerLeft is the independently derived fact the checker cross-references
	// against the book; if it disagreed, apply() would already have failed.
	if tr[0].MakerLeft != 380 {
		t.Fatalf("MakerLeft = %d, want 380", tr[0].MakerLeft)
	}
	if r.Snapshot.Resting[Sell] != 380 {
		t.Fatalf("resting sell = %d, want 380", r.Snapshot.Resting[Sell])
	}
}

func TestMultiLevelSweepConsumesCheapestFirst(t *testing.T) {
	b := newBook()
	apply(t, b, sellAs("m1", 10250, 100))
	apply(t, b, sellAs("m2", 10255, 100))
	apply(t, b, sellAs("m3", 10260, 100))

	r := apply(t, b, buyAs("taker", 10260, 250))

	tr := trades(r)
	if len(tr) != 3 {
		t.Fatalf("got %d trades, want 3", len(tr))
	}
	wantPx := []Ticks{10250, 10255, 10260}
	wantQty := []Qty{100, 100, 50}
	for i := range tr {
		if tr[i].Price != wantPx[i] || tr[i].Qty != wantQty[i] {
			t.Fatalf("trade %d = %d @ %d, want %d @ %d", i, tr[i].Qty, tr[i].Price, wantQty[i], wantPx[i])
		}
	}
	if r.Snapshot.Resting[Sell] != 50 {
		t.Fatalf("resting sell = %d, want 50", r.Snapshot.Resting[Sell])
	}
}

// A taker that crosses gets the passive side's price. It never pays its own
// limit when the resting order is better: the maker was there first and set the
// terms.
func TestPriceImprovementGoesToTheTaker(t *testing.T) {
	b := newBook()
	apply(t, b, sellAs("maker", 10250, 100))
	r := apply(t, b, buyAs("taker", 10300, 100)) // willing to pay 103.00

	tr := trades(r)
	if len(tr) != 1 || tr[0].Price != 10250 {
		t.Fatalf("traded at %d, want 10250 (the maker's price)", tr[0].Price)
	}
}

func TestSellTakerMirrorsBuyTaker(t *testing.T) {
	b := newBook()
	apply(t, b, buyAs("m1", 10250, 100))
	apply(t, b, buyAs("m2", 10245, 100))
	apply(t, b, buyAs("m3", 10240, 100))

	r := apply(t, b, sellAs("taker", 10240, 250))

	tr := trades(r)
	if len(tr) != 3 {
		t.Fatalf("got %d trades, want 3", len(tr))
	}
	// A sell taker eats the RICHEST bids first.
	wantPx := []Ticks{10250, 10245, 10240}
	for i := range tr {
		if tr[i].Price != wantPx[i] {
			t.Fatalf("trade %d at %d, want %d", i, tr[i].Price, wantPx[i])
		}
	}
	if r.Snapshot.Resting[Buy] != 50 {
		t.Fatalf("resting buy = %d, want 50", r.Snapshot.Resting[Buy])
	}
}

func TestNoTradeThroughTheLimit(t *testing.T) {
	b := newBook()
	apply(t, b, sellAs("m1", 10250, 100))
	apply(t, b, sellAs("m2", 10275, 100))

	r := apply(t, b, buyAs("taker", 10260, 300)) // will not pay 102.75

	tr := trades(r)
	if len(tr) != 1 || tr[0].Price != 10250 {
		t.Fatalf("got %+v, want a single fill at 10250", tr)
	}
	x, _ := rested(r)
	if x.Open != 200 {
		t.Fatalf("rested %d, want 200", x.Open)
	}
	// The remainder rests at 10260, below the 10275 ask: not crossed.
	if r.Snapshot.Bids[0].Price != 10260 || r.Snapshot.Asks[0].Price != 10275 {
		t.Fatalf("book = %v / %v", r.Snapshot.Bids, r.Snapshot.Asks)
	}
}

func TestBuyDoesNotMatchHigherAsk(t *testing.T) {
	b := newBook()
	apply(t, b, sell(10275, 100))
	r := apply(t, b, buy(10250, 100))

	if len(trades(r)) != 0 {
		t.Fatalf("crossed when it should not have: %+v", trades(r))
	}
}

func TestOneTakerFillsSeveralMakersAtOnePrice(t *testing.T) {
	b := newBook()
	apply(t, b, sellAs("m1", 10250, 40))
	apply(t, b, sellAs("m2", 10250, 60))
	apply(t, b, sellAs("m3", 10250, 90))

	r := apply(t, b, buyAs("taker", 10250, 130))

	tr := trades(r)
	if len(tr) != 3 {
		t.Fatalf("got %d trades, want 3", len(tr))
	}
	if tr[0].Qty != 40 || tr[1].Qty != 60 || tr[2].Qty != 30 {
		t.Fatalf("quantities %d/%d/%d, want 40/60/30", tr[0].Qty, tr[1].Qty, tr[2].Qty)
	}
	if tr[2].MakerLeft != 60 {
		t.Fatalf("last maker left %d, want 60", tr[2].MakerLeft)
	}
}
