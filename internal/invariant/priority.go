package invariant

// checkCrossed asserts the book is not crossed: the best bid must be strictly
// below the best ask. A crossed book means matchable liquidity was left resting,
// which is the visible symptom of almost every matching bug.
func checkCrossed(w walked, add func(Kind, string, int64, int64)) {
	if !w.has[Buy] || !w.has[Sell] {
		return
	}
	if w.best[Buy] >= w.best[Sell] {
		add(NoCrossedBook, "best bid is not below best ask", w.best[Sell], w.best[Buy])
	}
}

// checkPriority verifies price-time priority as a genuine POST-CONDITION.
//
// The trap: you cannot verify "the right order was matched" by inspecting the
// post-state, because the order that was matched is gone. So the checker needs
// evidence that exists at check time and does not come from the matcher's
// internals. There are three sources, and none of them is the matcher:
//
//  1. the O(1) frontier photographed BEFORE the command ran (the destroyed
//     pre-image),
//  2. the emitted trade list, read as an outside observer would read it,
//  3. the post-state structure, walked through the read-only Book interface
//     (checked in walkSide: levels ordered, queues in arrival order).
//
// The proof is an induction over the sweep.
func checkPriority(in Input, w walked, add func(Kind, string, int64, int64)) {
	if len(in.Fills) == 0 || !in.Taker.Present {
		return
	}
	opp := 1 - in.Taker.Side
	f0 := in.Fills[0]

	// BASE CASE. The strongest single check in the system: the first fill must
	// hit the true head of the true best level on the opposite side. If the
	// matcher skipped a better price, or jumped the queue at the right price,
	// exactly one of these two comparisons fails.
	if in.Pre[opp].Empty {
		add(PriceTimePriority, "filled against a side that was empty before the command", 0, f0.Qty)
	} else {
		if f0.MakerSeq != in.Pre[opp].HeadSeq {
			add(PriceTimePriority, "first fill did not hit the head of the queue",
				int64(in.Pre[opp].HeadSeq), int64(f0.MakerSeq))
		}
		if f0.Price != in.Pre[opp].Price {
			add(PriceTimePriority, "first fill did not happen at the best price",
				in.Pre[opp].Price, f0.Price)
		}
	}

	// INDUCTIVE STEP, over the trade list alone.
	for i := 1; i < len(in.Fills); i++ {
		prev, cur := in.Fills[i-1], in.Fills[i]
		if cur.Price == prev.Price {
			// Within one price, strictly increasing arrival: FIFO.
			if cur.MakerSeq <= prev.MakerSeq {
				add(PriceTimePriority, "fills within a price level are out of arrival order",
					int64(prev.MakerSeq), int64(cur.MakerSeq))
			}
			continue
		}
		// Across prices, the sweep must move AWAY from the taker, never back
		// towards a better one. A buy taker eats the cheapest asks first, so
		// prices are non-decreasing; a sell taker eats the richest bids first,
		// so prices are non-increasing. Moving the other way means a level was
		// skipped and then revisited.
		wrongWay := (in.Taker.Side == Buy && cur.Price < prev.Price) ||
			(in.Taker.Side == Sell && cur.Price > prev.Price)
		if wrongWay {
			add(PriceTimePriority, "sweep revisited a better price after leaving it",
				prev.Price, cur.Price)
		}
	}

	// Every fill must be at a price the taker was actually willing to pay.
	for _, f := range in.Fills {
		worse := (in.Taker.Side == Buy && f.Price > in.Taker.Price) ||
			(in.Taker.Side == Sell && f.Price < in.Taker.Price)
		if worse {
			add(PriceTimePriority, "fill outside the taker's limit", in.Taker.Price, f.Price)
		}
		if f.Qty <= 0 {
			add(PriceTimePriority, "non-positive fill quantity", 1, f.Qty)
		}
	}
}

// checkSweep asserts the aggressor did not stop early.
//
// If the taker still had quantity left over — which for a limit order means the
// remainder rested — then no crossable liquidity may remain on the opposite
// side. Resting behind liquidity you were willing to cross is the definition of
// an incomplete sweep.
//
// checkCrossed would also fire in most such cases, but it says "the book is
// crossed" where this says "the sweep terminated with matchable liquidity still
// available". During a live demo, the second sentence is the one that tells you
// where to look.
func checkSweep(in Input, w walked, add func(Kind, string, int64, int64)) {
	if !in.Taker.Present || in.Taker.Left <= 0 {
		return
	}
	opp := 1 - in.Taker.Side
	if !w.has[opp] {
		return
	}
	stillCrossable := (in.Taker.Side == Buy && w.best[opp] <= in.Taker.Price) ||
		(in.Taker.Side == Sell && w.best[opp] >= in.Taker.Price)
	if stillCrossable {
		add(SweepComplete, "taker rested with crossable liquidity still on the book",
			in.Taker.Price, w.best[opp])
	}
}
