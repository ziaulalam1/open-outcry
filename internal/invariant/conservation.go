package invariant

// checkConservation is the load-bearing proof of the whole demo: no shares are
// created or destroyed, however badly delivery is behaving.
//
// The identity, per side, in whole shares:
//
//	submitted[S] == resting[S] + filled[S] + canceled[S]
//
// plus the two cross-side identities that are the actual statement that a trade
// is a TRANSFER rather than a creation:
//
//	filled[Buy]   == filled[Sell]
//	notional[Buy] == notional[Sell]
//
// The two sides of the first identity are produced by different code reading
// different things. `resting` is a full walk of the actual book. The rest is an
// O(1) fold over the events the engine emitted. Neither can cover for the other.
//
// Exact equality — `==`, not a tolerance — is only decidable because prices are
// int64 cents. Integer ticks and checkable invariants are the same requirement
// seen twice: with float prices this function could only ever assert "close".
func checkConservation(in Input, w walked, add func(Kind, string, int64, int64)) {
	l := in.Ledger
	for _, s := range [2]uint8{Buy, Sell} {
		name := "buy side"
		if s == Sell {
			name = "sell side"
		}
		want := l.Submitted[s]
		got := w.resting[s] + l.Filled[s] + l.Canceled[s]
		if want != got {
			add(Conservation, name, want, got)
		}
	}
	if l.Filled[Buy] != l.Filled[Sell] {
		add(Conservation, "filled quantity differs across sides", l.Filled[Buy], l.Filled[Sell])
	}
	if l.Notional[Buy] != l.Notional[Sell] {
		add(Conservation, "notional differs across sides", l.Notional[Buy], l.Notional[Sell])
	}

	checkMakerResidual(in, w, add)
}

// checkMakerResidual closes the one hole conservation cannot see.
//
// Conservation compares a book walk against an event fold, so it catches a maker
// left in the book after being fully filled, a maker removed with no trade
// emitted, a partial fill written back with the wrong remainder, a double
// insert, a level dropped with orders still in it. What it CANNOT catch is a
// matcher that computes a single wrong n := min(taker.Open, maker.Open) and then
// uses that same n for both the decrement and the emitted Trade.Qty — the two
// representations agree because they share one variable, and the identity
// balances at the wrong number.
//
// The fix is to make the event carry a second, independently derived fact.
// Traded.MakerLeft is the maker's open quantity AFTER the fill, as the engine
// believed it. Here we compare that claim against the maker's ACTUAL open
// quantity in the post-state book, and require the maker to be absent from the
// book if and only if MakerLeft is zero. A shared-n error moves the book but not
// the arithmetic relating the two, so it surfaces here.
//
// Knowing the hole and naming the check that plugs it is the difference between
// writing assertions and understanding them.
func checkMakerResidual(in Input, w walked, add func(Kind, string, int64, int64)) {
	if len(in.Fills) == 0 || w.seqOpen == nil {
		return
	}
	// A maker can be hit more than once in a single sweep only if it appears
	// twice in the book, which is itself a violation caught above. The LAST fill
	// against a given maker is the one whose MakerLeft describes the post-state.
	last := make(map[uint64]Fill, len(in.Fills))
	for _, f := range in.Fills {
		last[f.MakerSeq] = f
	}
	for seq, f := range last {
		open, present := w.seqOpen[seq]
		switch {
		case f.MakerLeft == 0 && present:
			add(Conservation, "maker "+itoa(int64(seq))+" reported fully filled but still rests", 0, open)
		case f.MakerLeft > 0 && !present:
			add(Conservation, "maker "+itoa(int64(seq))+" reported partially filled but left the book", f.MakerLeft, 0)
		case f.MakerLeft > 0 && present && open != f.MakerLeft:
			add(Conservation, "maker "+itoa(int64(seq))+" residual disagrees with book", f.MakerLeft, open)
		case f.MakerLeft < 0:
			add(Conservation, "maker "+itoa(int64(seq))+" reported negative residual", 0, f.MakerLeft)
		}
	}
}
