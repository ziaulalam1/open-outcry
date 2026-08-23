package engine

import "github.com/ziaulalam1/open-outcry/internal/invariant"

// crosses reports whether a taker at takerPrice is willing to trade against a
// resting order at restingPrice.
func crosses(side Side, takerPrice, restingPrice Ticks) bool {
	if side == Buy {
		return takerPrice >= restingPrice
	}
	return takerPrice <= restingPrice
}

// match sweeps the opposite side, best price first and front of queue first,
// until the taker is exhausted or the next level is no longer crossable.
//
// Price-time priority is not enforced by a check in here; it is a consequence of
// two structural facts. Price: `opp.best()` is always the best level, because
// ladder.insert keeps levels sorted. Time: `lv.orders[0]` is always the earliest
// arrival, because insert only ever appends and this loop only ever consumes the
// front. The invariant checker then verifies the outcome independently, from the
// emitted trades and the pre-command frontier — it does not take this function's
// word for any of it.
func (b *Book) match(taker *Order) ([]invariant.Fill, []Event) {
	opp := b.opposite(taker.Side)

	var (
		fills  []invariant.Fill
		events []Event
	)

	for taker.Open > 0 {
		lv := opp.best()
		if lv == nil || !crosses(taker.Side, taker.Price, lv.price) {
			break
		}

		for taker.Open > 0 && len(lv.orders) > 0 {
			maker := lv.orders[0]

			n := taker.Open
			if maker.Open < n {
				n = maker.Open
			}

			maker.Open -= n
			taker.Open -= n
			lv.total -= n

			// The trade happens at the PASSIVE order's price. A taker that
			// crosses gets price improvement and never pays its own limit when
			// the resting side is better — the maker was there first and set the
			// terms.
			b.led.Fill(uint8(taker.Side), int64(n), int64(lv.price))

			t := Traded{
				Price:     lv.price,
				Qty:       n,
				TakerID:   taker.ID,
				MakerID:   maker.ID,
				TakerSeq:  taker.Seq,
				MakerSeq:  maker.Seq,
				Taker:     taker.Session,
				Maker:     maker.Session,
				TakerSide: taker.Side,
				MakerLeft: maker.Open,
			}
			events = append(events, t)
			b.lastTrade, b.hasTrade = t, true

			fills = append(fills, invariant.Fill{
				Price:     int64(lv.price),
				Qty:       int64(n),
				MakerSeq:  uint64(maker.Seq),
				MakerLeft: int64(maker.Open),
			})

			if maker.Open == 0 {
				delete(b.index, maker.ID)
				lv.popFront()
			}
		}

		// A drained level must not linger. An empty level is a queue position
		// nobody earned, and the next order at that price would inherit it.
		opp.dropBestIfEmpty()
	}

	return fills, events
}
