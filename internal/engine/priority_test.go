package engine

import "testing"

// The "time" half of price-time priority: at one price, earlier arrival fills
// first, and the only evidence of who was earlier is the arrival ordinal.
func TestSamePriceFillsInArrivalOrder(t *testing.T) {
	b := newBook()
	first := apply(t, b, sellAs("first", 10250, 100))
	second := apply(t, b, sellAs("second", 10250, 100))
	third := apply(t, b, sellAs("third", 10250, 100))

	firstSeq := mustRested(t, first).Seq
	secondSeq := mustRested(t, second).Seq
	thirdSeq := mustRested(t, third).Seq

	r := apply(t, b, buyAs("taker", 10250, 250))
	tr := trades(r)
	if len(tr) != 3 {
		t.Fatalf("got %d trades, want 3", len(tr))
	}
	if tr[0].MakerSeq != firstSeq || tr[1].MakerSeq != secondSeq || tr[2].MakerSeq != thirdSeq {
		t.Fatalf("filled in order %d/%d/%d, want %d/%d/%d",
			tr[0].MakerSeq, tr[1].MakerSeq, tr[2].MakerSeq, firstSeq, secondSeq, thirdSeq)
	}
	if tr[0].Maker != "first" || tr[1].Maker != "second" || tr[2].Maker != "third" {
		t.Fatalf("wrong sessions filled: %s/%s/%s", tr[0].Maker, tr[1].Maker, tr[2].Maker)
	}
}

// The "price" half: a later arrival at a better price jumps the whole queue.
func TestBetterPriceJumpsTheQueue(t *testing.T) {
	b := newBook()
	early := apply(t, b, sellAs("early", 10260, 100))
	late := apply(t, b, sellAs("late", 10250, 100)) // arrived later, priced better

	earlySeq := mustRested(t, early).Seq
	lateSeq := mustRested(t, late).Seq
	if lateSeq <= earlySeq {
		t.Fatal("test setup wrong: 'late' must have the higher sequence")
	}

	r := apply(t, b, buyAs("taker", 10260, 100))
	tr := trades(r)
	if len(tr) != 1 {
		t.Fatalf("got %d trades, want 1", len(tr))
	}
	if tr[0].MakerSeq != lateSeq {
		t.Fatalf("filled seq %d, want %d — the better price must win", tr[0].MakerSeq, lateSeq)
	}
}

// Removing an order must not disturb the relative order of the survivors.
func TestCancelPreservesPriorityOfSurvivors(t *testing.T) {
	b := newBook()
	a := apply(t, b, sellAs("a", 10250, 100))
	mid := apply(t, b, sellAs("b", 10250, 100))
	c := apply(t, b, sellAs("c", 10250, 100))

	aSeq := mustRested(t, a).Seq
	cSeq := mustRested(t, c).Seq
	apply(t, b, Cancel{Session: "b", ID: mustRested(t, mid).ID})

	r := apply(t, b, buyAs("taker", 10250, 200))
	tr := trades(r)
	if len(tr) != 2 {
		t.Fatalf("got %d trades, want 2", len(tr))
	}
	if tr[0].MakerSeq != aSeq || tr[1].MakerSeq != cSeq {
		t.Fatalf("filled %d then %d, want %d then %d", tr[0].MakerSeq, tr[1].MakerSeq, aSeq, cSeq)
	}
}

// A partially filled maker keeps its place at the front. Being hit does not send
// you to the back of the queue.
func TestPartiallyFilledMakerKeepsItsPlace(t *testing.T) {
	b := newBook()
	front := apply(t, b, sellAs("front", 10250, 200))
	behind := apply(t, b, sellAs("behind", 10250, 200))

	frontSeq := mustRested(t, front).Seq
	behindSeq := mustRested(t, behind).Seq

	apply(t, b, buyAs("t1", 10250, 50)) // nicks the front order
	r := apply(t, b, buyAs("t2", 10250, 200))

	tr := trades(r)
	if len(tr) != 2 {
		t.Fatalf("got %d trades, want 2", len(tr))
	}
	if tr[0].MakerSeq != frontSeq || tr[0].Qty != 150 {
		t.Fatalf("first fill seq %d qty %d, want %d qty 150", tr[0].MakerSeq, tr[0].Qty, frontSeq)
	}
	if tr[1].MakerSeq != behindSeq || tr[1].Qty != 50 {
		t.Fatalf("second fill seq %d qty %d, want %d qty 50", tr[1].MakerSeq, tr[1].Qty, behindSeq)
	}
}

// Re-entering after a cancel costs you your place — which is the point of
// time priority, and the reason cancel/replace is expensive at a real venue.
func TestReenteringGoesToTheBack(t *testing.T) {
	b := newBook()
	a := apply(t, b, sellAs("a", 10250, 100))
	bb := apply(t, b, sellAs("b", 10250, 100))

	apply(t, b, Cancel{Session: "a", ID: mustRested(t, a).ID})
	again := apply(t, b, sellAs("a", 10250, 100))

	bSeq := mustRested(t, bb).Seq
	aSeq := mustRested(t, again).Seq

	r := apply(t, b, buyAs("taker", 10250, 200))
	tr := trades(r)
	if tr[0].MakerSeq != bSeq || tr[1].MakerSeq != aSeq {
		t.Fatalf("filled %d then %d, want %d then %d", tr[0].MakerSeq, tr[1].MakerSeq, bSeq, aSeq)
	}
}

func mustRested(t *testing.T, r Result) Rested {
	t.Helper()
	x, ok := rested(r)
	if !ok {
		t.Fatalf("expected an order to rest, got %+v", r.Events)
	}
	return x
}
