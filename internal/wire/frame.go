// Package wire is the airlock.
//
// It is the ONLY package in this module that imports encoding/json or carries a
// json tag. Everything upstream of it trades in domain values; everything
// downstream trades in bytes. That is enforced by internal/arch, not by
// convention.
//
// The shapes here are not invented. They are the shapes the projector view was
// already built against, months of design decisions earlier in the build order:
// web/fixtures/tape.json is the hand-authored specification, and golden_test.go
// asserts that what this package emits still fits it. A field the projector
// reads but the server never sends is a test failure here rather than a blank
// space discovered on stage.
package wire

// PxScale is cents per unit. It travels on every frame carrying a price so the
// client never hardcodes it — the one hardcoded 100 in a UI is the one that
// survives into a system that later quotes in tenths of a cent.
const PxScale = 100

type levelDTO struct {
	Px  int64 `json:"px"`
	Qty int64 `json:"qty"`
}

type tradeDTO struct {
	Px   int64 `json:"px"`
	Qty  int64 `json:"qty"`
	Side int   `json:"side"`
}

type invDTO struct {
	Conservation bool `json:"conservation"`
	NoCross      bool `json:"no_cross"`
	Priority     bool `json:"priority"`
	Sweep        bool `json:"sweep"`
	Halted       bool `json:"halted"`
}

type totalsDTO struct {
	Submitted [2]int64 `json:"submitted"`
	Resting   [2]int64 `json:"resting"`
	Filled    [2]int64 `json:"filled"`
	Canceled  [2]int64 `json:"canceled"`
}

// bookFrame is the projector's view of the world.
//
// Self-contained top-N snapshots, never deltas. This is the single decision the
// whole of act two rests on: with deltas, a dropped frame leaves the degraded
// pane showing a book that was never true at any instant, and an attendee can
// correctly say "your book IS wrong". With snapshots, a dropped frame is
// conflation — the pane shows a coherent book that was genuinely true at
// AsOfMS. Delivery degrades; correctness does not.
type bookFrame struct {
	Pane    string     `json:"pane"`
	Type    string     `json:"type"`
	Seq     uint64     `json:"seq"`
	AsOfMS  int64      `json:"as_of_ms"`
	Actor   string     `json:"actor,omitempty"`
	PxScale int        `json:"px_scale"`
	Bids    [][3]int64 `json:"bids"` // [price, qty, orders]
	Asks    [][3]int64 `json:"asks"`
	Last    *tradeDTO  `json:"last"`
	HasLast bool       `json:"has_last"`
	Inv     invDTO     `json:"inv"`
	Totals  totalsDTO  `json:"totals"`
}

type statsFrame struct {
	Pane         string `json:"pane"`
	Type         string `json:"type"`
	Clients      int    `json:"clients"`
	Orders       int64  `json:"orders"`
	Backpressure uint64 `json:"backpressure"`
	ChaosDropped uint64 `json:"chaos_dropped"`
	ChaosDelayMS int    `json:"chaos_delay_ms"`
	Goroutines   int    `json:"goroutines"`
	EngineSeq    uint64 `json:"engine_seq"`
	Split        bool   `json:"split"`
}

type haltFrame struct {
	Pane      string       `json:"pane"`
	Type      string       `json:"type"`
	Seq       uint64       `json:"seq"`
	AsOfMS    int64        `json:"as_of_ms"`
	Inv       invDTO       `json:"inv"`
	Violation violationDTO `json:"violation"`
}

type violationDTO struct {
	Kind   string `json:"kind"`
	Detail string `json:"detail"`
	Want   int64  `json:"want"`
	Got    int64  `json:"got"`
}

// topFrame is what a phone gets: best bid, best ask, last trade. Not the ladder.
// A phone renders two prices; sending it eight price levels per side would be
// paying for bytes nothing displays, on the connection least able to afford it.
type topFrame struct {
	Type    string    `json:"type"`
	Seq     uint64    `json:"seq"`
	PxScale int       `json:"px_scale"`
	Bid     *levelDTO `json:"bid"`
	Ask     *levelDTO `json:"ask"`
	Last    *tradeDTO `json:"last"`
	HasLast bool      `json:"has_last"`
}

type fillFrame struct {
	Type    string `json:"type"`
	Seq     uint64 `json:"seq"`
	PxScale int    `json:"px_scale"`
	Side    string `json:"side"`
	Px      int64  `json:"px"`
	Qty     int64  `json:"qty"`
	OrderID uint64 `json:"order_id"`
}

type restedFrame struct {
	Type    string `json:"type"`
	Seq     uint64 `json:"seq"`
	PxScale int    `json:"px_scale"`
	Side    string `json:"side"`
	Px      int64  `json:"px"`
	Qty     int64  `json:"qty"`
	ID      uint64 `json:"id"`
	Session string `json:"session"`
}

type rejectFrame struct {
	Type    string `json:"type"`
	Seq     uint64 `json:"seq"`
	Reason  string `json:"reason"`
	Session string `json:"session"`
}

type helloFrame struct {
	Type    string   `json:"type"`
	Session string   `json:"session"`
	PxScale int      `json:"px_scale"`
	LanURL  string   `json:"lan_url,omitempty"`
	Title   string   `json:"title,omitempty"`
	Sub     string   `json:"subtitle,omitempty"`
	Roster  []string `json:"roster,omitempty"`
}
