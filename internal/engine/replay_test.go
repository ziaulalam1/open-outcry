package engine

import (
	"fmt"
	"strings"
	"testing"
)

// The engine has no clock, no goroutines and no map iteration in its output
// path, so the same command script must produce a byte-identical event stream
// every time. That is not a nice-to-have: it is what makes the fuzz target
// meaningful, what lets a failure be reproduced from a seed, and what would let
// the whole session be replayed from a log after the workshop.
func TestReplayIsDeterministic(t *testing.T) {
	script := replayScript()

	first, fills := runScript(script)
	for i := 0; i < 5; i++ {
		got, n := runScript(script)
		if got != first {
			t.Fatalf("run %d diverged from run 0", i+1)
		}
		if n != fills {
			t.Fatalf("run %d produced %d trades, run 0 produced %d", i+1, n, fills)
		}
	}
	// Count the trades rather than grepping the rendered stream: %+v on an
	// Event slice prints struct fields without type names, so a string search
	// for "Traded" can never match and the guard would pass vacuously forever.
	if fills == 0 {
		t.Fatal("script does not exercise the matcher")
	}
	t.Logf("%d commands, %d trades, stable %d-byte event stream", len(script), fills, len(first))
}

// Two books fed the same script must also agree with each other — the same
// property from the other direction, and the one that would catch state leaking
// through a package-level variable.
func TestTwoBooksAgree(t *testing.T) {
	script := replayScript()
	a, b := New(Config{Depth: 8}), New(Config{Depth: 8})

	for i, c := range script {
		ra, rb := a.Apply(c), b.Apply(c)
		if !ra.OK() || !rb.OK() {
			t.Fatalf("step %d: invariant broke", i)
		}
		if fmt.Sprintf("%+v", ra.Events) != fmt.Sprintf("%+v", rb.Events) {
			t.Fatalf("step %d: books disagree\n a: %+v\n b: %+v", i, ra.Events, rb.Events)
		}
		if fmt.Sprintf("%+v", ra.Snapshot) != fmt.Sprintf("%+v", rb.Snapshot) {
			t.Fatalf("step %d: snapshots disagree", i)
		}
	}
}

func runScript(script []Command) (string, int) {
	b := New(Config{Depth: 8})
	var sb strings.Builder
	fills := 0
	for _, c := range script {
		r := b.Apply(c)
		fills += len(trades(r))
		fmt.Fprintf(&sb, "%d|%+v|%v\n", r.Seq, r.Events, r.Report.OK())
	}
	return sb.String(), fills
}

func replayScript() []Command {
	rng := lcg(0xc0ffee)
	out := make([]Command, 0, 400)
	for i := 0; i < 400; i++ {
		side := Buy
		if rng.intn(2) == 1 {
			side = Sell
		}
		out = append(out, Submit{
			Session: SessionID(fmt.Sprintf("s%d", rng.intn(12))),
			Side:    side,
			Price:   Ticks(10250 - 30 + rng.intn(60)),
			Qty:     Qty((1 + rng.intn(5)) * 50),
		})
	}
	return out
}
