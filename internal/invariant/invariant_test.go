package invariant

import "testing"

// ── fakes ───────────────────────────────────────────────────────────────────
//
// The checker only ever sees these interfaces, which is what makes corrupting a
// book for a test trivial: there is no engine to fight. Every "impossible" state
// below is one a real matching bug would produce, and the point of this file is
// to prove each check FIRES on it. A checker that has never been seen to fail is
// indistinguishable from `return true`.

type fakeLevel struct {
	price  int64
	orders []OrderRef
}

func (l fakeLevel) Price() int64      { return l.price }
func (l fakeLevel) Len() int          { return len(l.orders) }
func (l fakeLevel) At(i int) OrderRef { return l.orders[i] }

type fakeLadder []fakeLevel

func (d fakeLadder) Len() int          { return len(d) }
func (d fakeLadder) Level(i int) Level { return d[i] }

type fakeBook struct{ bids, asks fakeLadder }

func (b fakeBook) Bids() Ladder { return b.bids }
func (b fakeBook) Asks() Ladder { return b.asks }

func lvl(price int64, refs ...OrderRef) fakeLevel {
	return fakeLevel{price: price, orders: refs}
}

func ord(seq uint64, open int64) OrderRef { return OrderRef{Seq: seq, Open: open} }

// cleanBook: one bid of 300 at 102.40, one ask of 400 at 102.60.
func cleanBook() fakeBook {
	return fakeBook{
		bids: fakeLadder{lvl(10240, ord(1, 300))},
		asks: fakeLadder{lvl(10260, ord(2, 400))},
	}
}

func cleanLedger() Ledger {
	l := Ledger{}
	l.Accept(Buy, 300)
	l.Accept(Sell, 400)
	return l
}

func base() Input {
	return Input{Book: cleanBook(), Ledger: cleanLedger(), Seq: 1}
}

func mustFire(t *testing.T, r Report, k Kind) {
	t.Helper()
	if r.OK() {
		t.Fatalf("expected %s to fire; report was clean", k)
	}
	for _, v := range r.Violations {
		if v.Kind == k {
			return
		}
	}
	t.Fatalf("expected %s; got %s", k, r)
}

func mustPass(t *testing.T, r Report) {
	t.Helper()
	if !r.OK() {
		t.Fatalf("expected a clean report; got %s", r)
	}
}

// ── the negative control ────────────────────────────────────────────────────

func TestCleanBookPasses(t *testing.T) {
	mustPass(t, Check(base()))
}

func TestCleanBookWithFillsPasses(t *testing.T) {
	in := base()
	// A buy taker for 100 hits the ask at 10260, leaving 300 of it resting.
	in.Book = fakeBook{
		bids: fakeLadder{lvl(10240, ord(1, 300))},
		asks: fakeLadder{lvl(10260, ord(2, 300))},
	}
	l := cleanLedger()
	l.Accept(Buy, 100)
	l.Fill(Buy, 100, 10260)
	in.Ledger = l
	in.Pre = [2]Frontier{
		Buy:  {Price: 10240, HeadSeq: 1, HeadOpen: 300},
		Sell: {Price: 10260, HeadSeq: 2, HeadOpen: 400},
	}
	in.Fills = []Fill{{Price: 10260, Qty: 100, MakerSeq: 2, MakerLeft: 300}}
	in.Taker = Taker{Present: true, Side: Buy, Price: 10260, Left: 0}
	mustPass(t, Check(in))
}

// ── conservation ────────────────────────────────────────────────────────────

func TestConservationFiresWhenSharesVanish(t *testing.T) {
	in := base()
	// The book holds 200 where the ledger says 300 was submitted and nothing
	// was filled or cancelled: 100 shares were destroyed.
	in.Book = fakeBook{
		bids: fakeLadder{lvl(10240, ord(1, 200))},
		asks: fakeLadder{lvl(10260, ord(2, 400))},
	}
	mustFire(t, Check(in), Conservation)
}

func TestConservationFiresWhenSharesAppear(t *testing.T) {
	in := base()
	in.Book = fakeBook{
		bids: fakeLadder{lvl(10240, ord(1, 300), ord(3, 50))}, // 50 nobody submitted
		asks: fakeLadder{lvl(10260, ord(2, 400))},
	}
	mustFire(t, Check(in), Conservation)
}

// A trade is a transfer. Crediting one side and not the other is the classic
// way to "create" shares while every per-side total still looks plausible.
func TestConservationFiresWhenOnlyOneSideIsCredited(t *testing.T) {
	in := base()
	l := Ledger{}
	l.Accept(Buy, 300)
	l.Accept(Sell, 400)
	l.Filled[Buy] += 100 // credited by hand, bypassing Fill()
	in.Ledger = l
	mustFire(t, Check(in), Conservation)
}

func TestConservationFiresOnNotionalMismatch(t *testing.T) {
	in := base()
	l := cleanLedger()
	l.Filled[Buy] += 100
	l.Filled[Sell] += 100
	l.Notional[Buy] += 10260 * 100
	l.Notional[Sell] += 10250 * 100 // the two sides traded at different prices
	in.Ledger = l
	// resting must still balance for this to isolate the notional check
	in.Book = fakeBook{
		bids: fakeLadder{lvl(10240, ord(1, 200))},
		asks: fakeLadder{lvl(10260, ord(2, 300))},
	}
	r := Check(in)
	found := false
	for _, v := range r.Violations {
		if v.Kind == Conservation && v.Detail == "notional differs across sides" {
			found = true
		}
	}
	if !found {
		t.Fatalf("notional check did not fire: %s", r)
	}
}

// ── the maker-residual cross-check ──────────────────────────────────────────
//
// This is the check that closes conservation's blind spot: a matcher using one
// wrong quantity for both the book decrement and the emitted trade size keeps
// the identity balanced at the wrong number.

func TestMakerResidualFiresWhenEventDisagreesWithBook(t *testing.T) {
	in := base()
	in.Book = fakeBook{
		bids: fakeLadder{lvl(10240, ord(1, 300))},
		asks: fakeLadder{lvl(10260, ord(2, 250))}, // book says 250 left
	}
	l := cleanLedger()
	l.Accept(Buy, 150)
	l.Fill(Buy, 150, 10260)
	in.Ledger = l
	in.Pre = [2]Frontier{
		Buy:  {Price: 10240, HeadSeq: 1, HeadOpen: 300},
		Sell: {Price: 10260, HeadSeq: 2, HeadOpen: 400},
	}
	in.Fills = []Fill{{Price: 10260, Qty: 150, MakerSeq: 2, MakerLeft: 300}} // event says 300
	in.Taker = Taker{Present: true, Side: Buy, Price: 10260}
	mustFire(t, Check(in), Conservation)
}

func TestMakerResidualFiresWhenFullyFilledMakerStillRests(t *testing.T) {
	in := base()
	l := cleanLedger()
	l.Accept(Buy, 400)
	l.Fill(Buy, 400, 10260)
	in.Ledger = l
	in.Book = fakeBook{
		bids: fakeLadder{lvl(10240, ord(1, 300))},
		asks: fakeLadder{lvl(10260, ord(2, 400))}, // should have been removed
	}
	in.Pre = [2]Frontier{
		Buy:  {Price: 10240, HeadSeq: 1, HeadOpen: 300},
		Sell: {Price: 10260, HeadSeq: 2, HeadOpen: 400},
	}
	in.Fills = []Fill{{Price: 10260, Qty: 400, MakerSeq: 2, MakerLeft: 0}}
	in.Taker = Taker{Present: true, Side: Buy, Price: 10260}
	mustFire(t, Check(in), Conservation)
}

// ── crossed book ────────────────────────────────────────────────────────────

func TestCrossedBookFires(t *testing.T) {
	in := base()
	in.Book = fakeBook{
		bids: fakeLadder{lvl(10270, ord(1, 300))}, // bid above ask
		asks: fakeLadder{lvl(10260, ord(2, 400))},
	}
	mustFire(t, Check(in), NoCrossedBook)
}

func TestTouchingBookIsCrossed(t *testing.T) {
	in := base()
	in.Book = fakeBook{
		bids: fakeLadder{lvl(10260, ord(1, 300))},
		asks: fakeLadder{lvl(10260, ord(2, 400))},
	}
	mustFire(t, Check(in), NoCrossedBook)
}

// ── structural priority ─────────────────────────────────────────────────────

func TestLevelsOutOfOrderFires(t *testing.T) {
	in := base()
	in.Book = fakeBook{
		bids: fakeLadder{lvl(10230, ord(1, 100)), lvl(10240, ord(3, 100))}, // ascending bids
		asks: fakeLadder{lvl(10260, ord(2, 400))},
	}
	in.Ledger.Submitted[Buy] = 200
	mustFire(t, Check(in), PriceTimePriority)
}

func TestDuplicateLevelFires(t *testing.T) {
	in := base()
	in.Book = fakeBook{
		bids: fakeLadder{lvl(10240, ord(1, 150)), lvl(10240, ord(3, 150))}, // split FIFO
		asks: fakeLadder{lvl(10260, ord(2, 400))},
	}
	mustFire(t, Check(in), PriceTimePriority)
}

func TestQueueOutOfArrivalOrderFires(t *testing.T) {
	in := base()
	in.Book = fakeBook{
		bids: fakeLadder{lvl(10240, ord(9, 150), ord(4, 150))}, // later arrival in front
		asks: fakeLadder{lvl(10260, ord(2, 400))},
	}
	mustFire(t, Check(in), PriceTimePriority)
}

func TestEmptyLevelFires(t *testing.T) {
	in := base()
	in.Book = fakeBook{
		bids: fakeLadder{lvl(10240, ord(1, 300)), lvl(10235)}, // a level with no orders
		asks: fakeLadder{lvl(10260, ord(2, 400))},
	}
	mustFire(t, Check(in), PriceTimePriority)
}

func TestNonPositiveRestingQuantityFires(t *testing.T) {
	in := base()
	in.Book = fakeBook{
		bids: fakeLadder{lvl(10240, ord(1, 300), ord(3, 0))},
		asks: fakeLadder{lvl(10260, ord(2, 400))},
	}
	mustFire(t, Check(in), PriceTimePriority)
}

// ── the sweep witness ───────────────────────────────────────────────────────

func TestFirstFillMustHitTheHeadOfTheQueue(t *testing.T) {
	in := base()
	in.Pre = [2]Frontier{
		Buy:  {Price: 10240, HeadSeq: 1, HeadOpen: 300},
		Sell: {Price: 10260, HeadSeq: 2, HeadOpen: 400},
	}
	// Filled seq 7 when seq 2 was at the front: the queue was jumped.
	in.Fills = []Fill{{Price: 10260, Qty: 100, MakerSeq: 7, MakerLeft: 0}}
	in.Taker = Taker{Present: true, Side: Buy, Price: 10260}
	mustFire(t, Check(in), PriceTimePriority)
}

func TestFirstFillMustHappenAtTheBestPrice(t *testing.T) {
	in := base()
	in.Pre = [2]Frontier{
		Buy:  {Price: 10240, HeadSeq: 1, HeadOpen: 300},
		Sell: {Price: 10260, HeadSeq: 2, HeadOpen: 400},
	}
	in.Fills = []Fill{{Price: 10275, Qty: 100, MakerSeq: 2, MakerLeft: 0}} // skipped 10260
	in.Taker = Taker{Present: true, Side: Buy, Price: 10300}
	mustFire(t, Check(in), PriceTimePriority)
}

func TestFillsWithinAPriceMustAdvanceInArrivalOrder(t *testing.T) {
	in := base()
	in.Pre = [2]Frontier{Sell: {Price: 10260, HeadSeq: 2, HeadOpen: 100}}
	in.Fills = []Fill{
		{Price: 10260, Qty: 100, MakerSeq: 2, MakerLeft: 0},
		{Price: 10260, Qty: 100, MakerSeq: 1, MakerLeft: 0}, // went backwards
	}
	in.Taker = Taker{Present: true, Side: Buy, Price: 10260}
	mustFire(t, Check(in), PriceTimePriority)
}

func TestSweepMustNotRevisitABetterPrice(t *testing.T) {
	in := base()
	in.Pre = [2]Frontier{Sell: {Price: 10250, HeadSeq: 2, HeadOpen: 100}}
	in.Fills = []Fill{
		{Price: 10250, Qty: 100, MakerSeq: 2, MakerLeft: 0},
		{Price: 10270, Qty: 100, MakerSeq: 3, MakerLeft: 0},
		{Price: 10260, Qty: 100, MakerSeq: 4, MakerLeft: 0}, // back to a better price
	}
	in.Taker = Taker{Present: true, Side: Buy, Price: 10300}
	mustFire(t, Check(in), PriceTimePriority)
}

func TestFillOutsideTheTakersLimitFires(t *testing.T) {
	in := base()
	in.Pre = [2]Frontier{Sell: {Price: 10260, HeadSeq: 2, HeadOpen: 400}}
	in.Fills = []Fill{{Price: 10260, Qty: 100, MakerSeq: 2, MakerLeft: 300}}
	in.Taker = Taker{Present: true, Side: Buy, Price: 10250} // would not pay 10260
	mustFire(t, Check(in), PriceTimePriority)
}

// ── sweep completeness ──────────────────────────────────────────────────────

func TestSweepIncompleteFires(t *testing.T) {
	in := base()
	// The taker rested at 10260 while an ask sits at 10260: it stopped early.
	in.Book = fakeBook{
		bids: fakeLadder{lvl(10260, ord(3, 100))},
		asks: fakeLadder{lvl(10260, ord(2, 400))},
	}
	in.Taker = Taker{Present: true, Side: Buy, Price: 10260, Left: 100}
	r := Check(in)
	mustFire(t, r, SweepComplete)
	// It is also a crossed book. Both firing is correct and useful: one says
	// what is wrong, the other says why.
	mustFire(t, r, NoCrossedBook)
}

func TestRestingBehindTheSpreadIsFine(t *testing.T) {
	in := base()
	in.Taker = Taker{Present: true, Side: Buy, Price: 10240, Left: 300}
	mustPass(t, Check(in))
}

// ── report plumbing ─────────────────────────────────────────────────────────

func TestReportNamesEveryFailedCheck(t *testing.T) {
	in := base()
	in.Book = fakeBook{
		bids: fakeLadder{lvl(10270, ord(9, 50), ord(4, 50))},
		asks: fakeLadder{lvl(10260, ord(2, 400))},
	}
	r := Check(in)
	if r.OK() {
		t.Fatal("expected violations")
	}
	if r.Passed[Conservation] || r.Passed[NoCrossedBook] || r.Passed[PriceTimePriority] {
		// All three should be marked failed; SweepComplete has nothing to say.
		t.Fatalf("Passed flags not set correctly: %+v", r.Passed)
	}
	if !r.Passed[SweepComplete] {
		t.Fatal("SweepComplete should still be passing")
	}
	if r.String() == "" {
		t.Fatal("report should render")
	}
}

func TestEmptyBookIsValid(t *testing.T) {
	mustPass(t, Check(Input{Book: fakeBook{}, Seq: 1}))
}
