package engine

// level is one price with its FIFO queue.
//
// orders[0] is the front of the queue: the earliest arrival, and therefore the
// next to be filled. Nothing in this package is permitted to reorder that slice
// except by removing from the front or appending to the back.
type level struct {
	price  Ticks
	orders []*Order
	total  Qty // sum of orders[i].Open — maintained here, and NEVER shown to the checker
}

func (l *level) push(o *Order) {
	l.orders = append(l.orders, o)
	l.total += o.Open
}

// popFront removes the fully-filled head. Only ever called on an order whose
// Open has reached zero.
func (l *level) popFront() {
	l.orders[0] = nil // let the order be collected; the index no longer holds it
	l.orders = l.orders[1:]
}

func (l *level) remove(id OrderID) (*Order, bool) {
	for i, o := range l.orders {
		if o.ID != id {
			continue
		}
		l.total -= o.Open
		l.orders = append(l.orders[:i], l.orders[i+1:]...)
		return o, true
	}
	return nil, false
}

// ladder is one side of the book: price levels held best-first.
//
// A slice, not a heap or a tree. At workshop scale a side holds on the order of
// twenty levels, so an O(levels) insert that keeps the slice sorted is both
// faster than a tree and — the reason that matters here — obviously correct by
// inspection. The scaling answer belongs in the README, not in this file.
type ladder struct {
	side   Side
	levels []*level
}

// better reports whether price a comes before price b on this side.
func (d *ladder) better(a, b Ticks) bool {
	if d.side == Buy {
		return a > b // higher bids first
	}
	return a < b // lower asks first
}

func (d *ladder) best() *level {
	if len(d.levels) == 0 {
		return nil
	}
	return d.levels[0]
}

// insert appends the order to its price level, creating the level if needed and
// keeping levels sorted best-first. Appending is what preserves time priority:
// a new order at an existing price always joins the BACK of that queue.
func (d *ladder) insert(o *Order) {
	for i, lv := range d.levels {
		if lv.price == o.Price {
			lv.push(o)
			return
		}
		if d.better(o.Price, lv.price) {
			nl := &level{price: o.Price}
			nl.push(o)
			d.levels = append(d.levels, nil)
			copy(d.levels[i+1:], d.levels[i:])
			d.levels[i] = nl
			return
		}
	}
	nl := &level{price: o.Price}
	nl.push(o)
	d.levels = append(d.levels, nl)
}

// dropBestIfEmpty removes the best level once its queue has drained. Levels must
// never linger empty: an empty level is a queue position nobody earned, and the
// checker treats one as a priority violation.
func (d *ladder) dropBestIfEmpty() {
	if len(d.levels) > 0 && len(d.levels[0].orders) == 0 {
		d.levels[0] = nil
		d.levels = d.levels[1:]
	}
}

func (d *ladder) removeOrder(o *Order) bool {
	for i, lv := range d.levels {
		if lv.price != o.Price {
			continue
		}
		if _, ok := lv.remove(o.ID); !ok {
			return false
		}
		if len(lv.orders) == 0 {
			d.levels = append(d.levels[:i], d.levels[i+1:]...)
		}
		return true
	}
	return false
}

// frontier photographs this side's best level for the checker. O(1), no copy.
// Taken BEFORE a command runs, it is the pre-image the command is about to
// destroy — and without it, price-time priority cannot be checked afterwards at
// all, because the order that got matched is gone.
func (d *ladder) frontier() invariantFrontier {
	lv := d.best()
	if lv == nil || len(lv.orders) == 0 {
		return invariantFrontier{Empty: true}
	}
	head := lv.orders[0]
	return invariantFrontier{
		Price:    int64(lv.price),
		HeadSeq:  uint64(head.Seq),
		HeadOpen: int64(head.Open),
	}
}
