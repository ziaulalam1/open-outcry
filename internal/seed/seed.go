// Package seed builds the opening ladder so the projector is never sparse.
//
// A book with three orders in it photographs badly and teaches nothing. The room
// arrives to a market that already looks like a market, and then changes it.
package seed

import "github.com/ziaulalam1/open-outcry/internal/engine"

// Session is the identity the baseline orders are entered under. It is a real
// session like any other, which means the baseline can be cancelled, traded
// against, and counted — it is not privileged.
const Session engine.SessionID = "house"

// Mid is the opening price in cents: $102.50.
const Mid engine.Ticks = 10250

// Baseline returns the commands that build the opening ladder.
//
// These are applied through the NORMAL Submit path. There is deliberately no
// constructor that injects orders straight into the book: a back door would put
// quantity in the ladder that the ledger never saw, conservation would fail on
// the very first check, and the tempting fix would be to weaken the check to
// accommodate it. Never weaken a check to accommodate a back door — remove the
// back door. The seed going through the front door is what keeps the identity
// true from sequence 1.
func Baseline() []engine.Command {
	const (
		levels = 7
		tick   = engine.Ticks(5)
		spread = engine.Ticks(10) // 10c wide at the open: visible from the back row
	)
	// Deterministic, so the opening book is byte-identical every run and the
	// golden test has a fixed target. Sizes vary per level only so the depth
	// bars on the projector have some shape to them.
	sizes := [levels]engine.Qty{300, 500, 400, 250, 450, 350, 200}

	out := make([]engine.Command, 0, levels*2)
	for i := 0; i < levels; i++ {
		out = append(out, engine.Submit{
			Session: Session,
			Side:    engine.Buy,
			Price:   Mid - spread/2 - engine.Ticks(i)*tick,
			Qty:     sizes[i],
		})
	}
	for i := 0; i < levels; i++ {
		out = append(out, engine.Submit{
			Session: Session,
			Side:    engine.Sell,
			Price:   Mid + spread/2 + engine.Ticks(i)*tick,
			Qty:     sizes[(i+3)%levels],
		})
	}
	return out
}
