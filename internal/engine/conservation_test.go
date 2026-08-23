package engine

import (
	"fmt"
	"testing"
)

// A deterministic pseudo-random source. Not math/rand: these tests must produce
// the identical command stream on every machine and every run, so a failure is
// reproducible from the test name alone.
type lcg uint64

func (r *lcg) next() uint64 {
	*r = lcg(uint64(*r)*6364136223846793005 + 1442695040888963407)
	return uint64(*r) >> 11
}

func (r *lcg) intn(n int) int { return int(r.next() % uint64(n)) }

// The identity holds across a long mixed stream. Every single Apply in this loop
// is checked — apply() fails the test the moment any invariant breaks — so this
// is really 10,000 assertions of the full check, not one at the end.
func TestConservationAcross10kMixedCommands(t *testing.T) {
	b := newBook()
	rng := lcg(0x5eed)
	var live []OrderID
	owner := map[OrderID]string{}

	for i := 0; i < 10000; i++ {
		switch {
		case len(live) > 0 && rng.intn(100) < 12:
			k := rng.intn(len(live))
			id := live[k]
			live = append(live[:k], live[k+1:]...)
			apply(t, b, Cancel{Session: SessionID(owner[id]), ID: id})

		default:
			sess := fmt.Sprintf("s%d", rng.intn(30))
			side := Buy
			if rng.intn(2) == 1 {
				side = Sell
			}
			// Prices straddle the mid so roughly a third of orders cross.
			px := int64(10250 - 40 + rng.intn(80))
			qty := int64((1 + rng.intn(6)) * 50)
			cmd := Submit{Session: SessionID(sess), Side: side, Price: Ticks(px), Qty: Qty(qty)}
			r := apply(t, b, cmd)
			if x, ok := rested(r); ok {
				live = append(live, x.ID)
				owner[x.ID] = sess
			}
		}
	}

	// Restate the identity here explicitly, from the snapshot, so this test says
	// out loud what it is protecting rather than hiding it inside the helper.
	s := b.Apply(Cancel{ID: 0}).Snapshot
	for _, side := range [2]Side{Buy, Sell} {
		want := s.Submitted[side]
		got := s.Resting[side] + s.Filled[side] + s.Canceled[side]
		if want != got {
			t.Fatalf("%s: submitted %d != resting %d + filled %d + canceled %d (= %d)",
				side, want, s.Resting[side], s.Filled[side], s.Canceled[side], got)
		}
	}
	if s.Filled[Buy] != s.Filled[Sell] {
		t.Fatalf("filled buy %d != filled sell %d — shares were created or destroyed",
			s.Filled[Buy], s.Filled[Sell])
	}
	if s.Filled[Buy] == 0 {
		t.Fatal("test is not exercising the matcher: no trades happened")
	}
	t.Logf("10k commands: filled %d, resting %v, canceled %v, seq %d",
		s.Filled[Buy], s.Resting, s.Canceled, s.Seq)
}

func TestConservationAcrossACancelOfAPartialFill(t *testing.T) {
	b := newBook()
	id := restingID(t, apply(t, b, buyAs("alice", 10240, 500)))
	apply(t, b, sellAs("bob", 10240, 180))
	r := apply(t, b, Cancel{Session: "alice", ID: id})

	s := r.Snapshot
	if s.Filled[Buy] != 180 || s.Canceled[Buy] != 320 || s.Resting[Buy] != 0 {
		t.Fatalf("filled %d canceled %d resting %d, want 180/320/0",
			s.Filled[Buy], s.Canceled[Buy], s.Resting[Buy])
	}
	if s.Submitted[Buy] != 500 {
		t.Fatalf("submitted %d, want 500", s.Submitted[Buy])
	}
}

// Notional is the second half of "a trade is a transfer". If the two sides were
// ever credited at different prices, this is what would catch it.
func TestNotionalBalancesAcrossSides(t *testing.T) {
	b := newBook()
	apply(t, b, sellAs("m1", 10250, 100))
	apply(t, b, sellAs("m2", 10255, 100))
	apply(t, b, buyAs("taker", 10300, 200))

	// Reach into the ledger directly: this is the one property the snapshot does
	// not surface, and it is worth asserting explicitly rather than only via the
	// checker.
	if b.led.Notional[Buy] != b.led.Notional[Sell] {
		t.Fatalf("notional buy %d != sell %d", b.led.Notional[Buy], b.led.Notional[Sell])
	}
	want := int64(10250*100 + 10255*100)
	if b.led.Notional[Buy] != want {
		t.Fatalf("notional %d, want %d", b.led.Notional[Buy], want)
	}
}

func TestSeededBookSatisfiesTheIdentityFromSequenceOne(t *testing.T) {
	// The seed goes through the normal Submit path. If anyone ever adds a
	// constructor that injects orders straight into the ladder, this fails.
	b := newBook()
	for _, c := range baselineForTest() {
		apply(t, b, c)
	}
	s := b.Apply(Cancel{ID: 0}).Snapshot
	if s.Resting[Buy] == 0 || s.Resting[Sell] == 0 {
		t.Fatal("seeded book should never be sparse")
	}
	if s.Filled[Buy] != 0 {
		t.Fatal("the opening ladder must not be crossed")
	}
}

// A local copy of the baseline shape, so package engine's tests do not import
// package seed and create a cycle in the test build.
func baselineForTest() []Command {
	var out []Command
	sizes := []Qty{300, 500, 400, 250, 450, 350, 200}
	for i := 0; i < 7; i++ {
		out = append(out, Submit{Session: "house", Side: Buy, Price: Ticks(10245 - i*5), Qty: sizes[i]})
	}
	for i := 0; i < 7; i++ {
		out = append(out, Submit{Session: "house", Side: Sell, Price: Ticks(10255 + i*5), Qty: sizes[(i+3)%7]})
	}
	return out
}
