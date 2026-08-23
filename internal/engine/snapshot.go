package engine

// Level is one aggregated price level, as the outside world sees it.
type Level struct {
	Price  Ticks
	Qty    Qty
	Orders int
}

// Snapshot is a self-contained picture of the book at one sequence number.
//
// THE ALIASING RULE, which this type exists to obey: no value that crosses a
// goroutine boundary may contain a pointer or a slice into live book state.
//
// So LastTrade is a value plus a bool, never a *Traded — a pointer would be
// eight bytes cheaper and would hand another goroutine a struct this one can
// rewrite. And Bids/Asks are freshly allocated on every call, never a reused
// scratch buffer, because reusing one would mean the engine mutating a slice
// that the encoder is concurrently reading. Both mistakes are invisible to
// `go test -race` when the engine's own tests are single-goroutine, which is
// exactly why the rule is written down here rather than left to judgement.
type Snapshot struct {
	Seq       Seq
	Bids      []Level
	Asks      []Level
	LastTrade Traded
	HasTrade  bool

	// Running totals, for display. The invariant checker does NOT read these —
	// it derives its own, which is the whole point of the exercise.
	Submitted [2]Qty
	Resting   [2]Qty
	Filled    [2]Qty
	Canceled  [2]Qty
}

func (b *Book) snapshot() Snapshot {
	s := Snapshot{
		Seq:       b.seq,
		Bids:      b.topLevels(&b.bids),
		Asks:      b.topLevels(&b.asks),
		LastTrade: b.lastTrade,
		HasTrade:  b.hasTrade,
	}
	for _, side := range [2]Side{Buy, Sell} {
		s.Submitted[side] = Qty(b.led.Submitted[side])
		s.Filled[side] = Qty(b.led.Filled[side])
		s.Canceled[side] = Qty(b.led.Canceled[side])
	}
	s.Resting[Buy] = restingOf(&b.bids)
	s.Resting[Sell] = restingOf(&b.asks)
	return s
}

// topLevels allocates a fresh slice every call. See the aliasing rule above.
func (b *Book) topLevels(d *ladder) []Level {
	n := b.cfg.Depth
	if len(d.levels) < n {
		n = len(d.levels)
	}
	out := make([]Level, 0, n)
	for i := 0; i < n; i++ {
		lv := d.levels[i]
		out = append(out, Level{Price: lv.price, Qty: lv.total, Orders: len(lv.orders)})
	}
	return out
}

func restingOf(d *ladder) Qty {
	var total Qty
	for _, lv := range d.levels {
		total += lv.total
	}
	return total
}
