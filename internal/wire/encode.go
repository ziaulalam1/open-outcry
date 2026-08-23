package wire

import (
	"encoding/json"

	"github.com/ziaulalam1/open-outcry/internal/engine"
	"github.com/ziaulalam1/open-outcry/internal/invariant"
)

// Encoder turns domain values into bytes. It holds no state and no clock: the
// caller supplies asOfMS, because the loop goroutine is the only thing that
// knows when a book state was produced, and stamping it anywhere further
// downstream would let the chaos decorator hide its own latency.
type Encoder struct{}

func New() Encoder { return Encoder{} }

func sideName(s engine.Side) string {
	if s == engine.Buy {
		return "buy"
	}
	return "sell"
}

func levels(in []engine.Level) [][3]int64 {
	// Freshly allocated. The aliasing rule holds across this boundary too: these
	// bytes are about to be handed to another goroutine.
	out := make([][3]int64, 0, len(in))
	for _, l := range in {
		out = append(out, [3]int64{int64(l.Price), int64(l.Qty), int64(l.Orders)})
	}
	return out
}

func trade(s engine.Snapshot) *tradeDTO {
	if !s.HasTrade {
		return nil
	}
	return &tradeDTO{Px: int64(s.LastTrade.Price), Qty: int64(s.LastTrade.Qty), Side: int(s.LastTrade.TakerSide)}
}

func report(r invariant.Report, halted bool) invDTO {
	return invDTO{
		Conservation: r.Passed[invariant.Conservation],
		NoCross:      r.Passed[invariant.NoCrossedBook],
		Priority:     r.Passed[invariant.PriceTimePriority],
		Sweep:        r.Passed[invariant.SweepComplete],
		Halted:       halted,
	}
}

func totals(s engine.Snapshot) totalsDTO {
	q := func(v [2]engine.Qty) [2]int64 { return [2]int64{int64(v[0]), int64(v[1])} }
	return totalsDTO{Submitted: q(s.Submitted), Resting: q(s.Resting), Filled: q(s.Filled), Canceled: q(s.Canceled)}
}

// Book encodes a full book snapshot for one pane.
func (Encoder) Book(pane string, r engine.Result, actor engine.SessionID, asOfMS int64, halted bool) []byte {
	f := bookFrame{
		Pane:    pane,
		Type:    "book",
		Seq:     uint64(r.Seq),
		AsOfMS:  asOfMS,
		Actor:   string(actor),
		PxScale: PxScale,
		Bids:    levels(r.Snapshot.Bids),
		Asks:    levels(r.Snapshot.Asks),
		Last:    trade(r.Snapshot),
		HasLast: r.Snapshot.HasTrade,
		Inv:     report(r.Report, halted),
		Totals:  totals(r.Snapshot),
	}
	b, _ := json.Marshal(f)
	return b
}

// Top encodes best bid and ask for phones.
func (Encoder) Top(r engine.Result, asOfMS int64) []byte {
	f := topFrame{Type: "top", Seq: uint64(r.Seq), PxScale: PxScale, HasLast: r.Snapshot.HasTrade}
	if len(r.Snapshot.Bids) > 0 {
		b := r.Snapshot.Bids[0]
		f.Bid = &levelDTO{Px: int64(b.Price), Qty: int64(b.Qty)}
	}
	if len(r.Snapshot.Asks) > 0 {
		a := r.Snapshot.Asks[0]
		f.Ask = &levelDTO{Px: int64(a.Price), Qty: int64(a.Qty)}
	}
	f.Last = trade(r.Snapshot)
	b, _ := json.Marshal(f)
	return b
}

// Halt encodes the frame the room sees when an invariant fails live.
func (Encoder) Halt(pane string, r engine.Result, asOfMS int64) []byte {
	f := haltFrame{Pane: pane, Type: "halt", Seq: uint64(r.Seq), AsOfMS: asOfMS, Inv: report(r.Report, true)}
	if len(r.Report.Violations) > 0 {
		v := r.Report.Violations[0]
		f.Violation = violationDTO{Kind: v.Kind.String(), Detail: v.Detail, Want: v.Want, Got: v.Got}
	}
	b, _ := json.Marshal(f)
	return b
}

// SessionFrames emits the per-trader frames an event implies.
//
// It takes a callback rather than returning a slice of some SessionFrame
// struct, because that struct would have to live in a package both this one and
// internal/loop could import — and any such package drags the wire format back
// across the boundary that internal/arch exists to defend.
//
// A trade produces two frames, one per counterparty. Both of them did
// something; both deserve to see it.
func (Encoder) SessionFrames(seq engine.Seq, ev engine.Event, emit func(session string, frame []byte)) {
	if ev == nil || emit == nil {
		return
	}
	switch e := ev.(type) {
	case engine.Traded:
		mk := func(sess engine.SessionID, side engine.Side, id engine.OrderID) {
			b, _ := json.Marshal(fillFrame{
				Type: "fill", Seq: uint64(seq), PxScale: PxScale,
				Side: sideName(side), Px: int64(e.Price), Qty: int64(e.Qty), OrderID: uint64(id),
			})
			emit(string(sess), b)
		}
		mk(e.Taker, e.TakerSide, e.TakerID)
		mk(e.Maker, e.TakerSide.Opposite(), e.MakerID)

	case engine.Rested:
		b, _ := json.Marshal(restedFrame{
			Type: "rested", Seq: uint64(seq), PxScale: PxScale,
			Side: sideName(e.Side), Px: int64(e.Price), Qty: int64(e.Open),
			ID: uint64(e.ID), Session: string(e.Session),
		})
		emit(string(e.Session), b)

	case engine.Canceled:
		b, _ := json.Marshal(restedFrame{
			Type: "canceled", Seq: uint64(seq), PxScale: PxScale,
			Side: sideName(e.Side), Px: int64(e.Price), Qty: int64(e.Released),
			ID: uint64(e.ID), Session: string(e.Session),
		})
		emit(string(e.Session), b)

	case engine.Rejected:
		b, _ := json.Marshal(rejectFrame{
			Type: "reject", Seq: uint64(seq), Reason: e.Reason.String(), Session: string(e.Session),
		})
		emit(string(e.Session), b)
	}
}

// Hello is the first frame a client receives.
func (Encoder) Hello(session, lanURL, title, sub string, roster []string) []byte {
	b, _ := json.Marshal(helloFrame{
		Type: "hello", Session: session, PxScale: PxScale,
		LanURL: lanURL, Title: title, Sub: sub, Roster: roster,
	})
	return b
}

// StatsInput is a plain value. The hub owns the counters inside it and composes
// this on its own goroutine; nothing reads those counters across a boundary.
type StatsInput struct {
	Clients      int
	Orders       int64
	Backpressure uint64
	ChaosDropped uint64
	ChaosDelayMS int
	Goroutines   int
	EngineSeq    uint64
	Split        bool
}

func (Encoder) Stats(s StatsInput) []byte {
	b, _ := json.Marshal(statsFrame{
		Pane: "fresh", Type: "stats",
		Clients: s.Clients, Orders: s.Orders, Backpressure: s.Backpressure,
		ChaosDropped: s.ChaosDropped, ChaosDelayMS: s.ChaosDelayMS,
		Goroutines: s.Goroutines, EngineSeq: s.EngineSeq, Split: s.Split,
	})
	return b
}

// Busy tells one trader their order did not reach the book.
//
// This exists because the inbound path must never borrow the outbound drop
// policy. Dropping a frame on the way to a screen costs freshness; dropping a
// command on the way to the book costs a trade that the trader believes
// happened. The first is the demo; the second would invalidate it.
func (Encoder) Busy(session string) []byte {
	b, _ := json.Marshal(rejectFrame{Type: "reject", Reason: "ENGINE_BUSY", Session: session})
	return b
}
