package engine

import "github.com/ziaulalam1/open-outcry/internal/invariant"

// This file is the entire cost of the package boundary between the engine and
// the checker: about thirty lines of adapters. It buys the only defensible
// answer to the question an interviewer actually asks — "couldn't the checker
// just read a counter the matcher already set?" — which is that it cannot name
// a single type in this package, so no.
//
// Note what is deliberately NOT adapted: level.total. The engine maintains a
// running per-level total, and exposing it would let the checker sum a number
// the matcher computed rather than the orders themselves. levelView offers only
// Price, Len and At, so conservation is derived from the individual orders and
// nothing else.

type invariantFrontier = invariant.Frontier

type bookView struct{ b *Book }

func (v bookView) Bids() invariant.Ladder { return ladderView{&v.b.bids} }
func (v bookView) Asks() invariant.Ladder { return ladderView{&v.b.asks} }

type ladderView struct{ d *ladder }

func (v ladderView) Len() int                    { return len(v.d.levels) }
func (v ladderView) Level(i int) invariant.Level { return levelView{v.d.levels[i]} }

type levelView struct{ lv *level }

func (v levelView) Price() int64 { return int64(v.lv.price) }
func (v levelView) Len() int     { return len(v.lv.orders) }

func (v levelView) At(i int) invariant.OrderRef {
	o := v.lv.orders[i]
	return invariant.OrderRef{Seq: uint64(o.Seq), Open: int64(o.Open)}
}
