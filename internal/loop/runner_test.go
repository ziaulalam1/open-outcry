package loop

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ziaulalam1/open-outcry/internal/engine"
	"github.com/ziaulalam1/open-outcry/internal/invariant"
	"go.uber.org/goleak"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

// ── fakes ───────────────────────────────────────────────────────────────────

type capture struct {
	mu     sync.Mutex
	frames map[string][]string
	refuse bool
}

func newCapture() *capture { return &capture{frames: map[string][]string{}} }

func (c *capture) Publish(topic string, b []byte) bool {
	if c.refuse {
		return false
	}
	c.mu.Lock()
	c.frames[topic] = append(c.frames[topic], string(b))
	c.mu.Unlock()
	return true
}

func (c *capture) count(topic string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.frames[topic])
}

func (c *capture) topics() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, 0, len(c.frames))
	for k := range c.frames {
		out = append(out, k)
	}
	return out
}

// fakeEnc renders just enough to tell frames apart.
type fakeEnc struct{}

func (fakeEnc) Book(pane string, r engine.Result, actor engine.SessionID, asOfMS int64, halted bool) []byte {
	return []byte("book:" + pane + ":" + string(actor))
}
func (fakeEnc) Top(r engine.Result, asOfMS int64) []byte { return []byte("top") }
func (fakeEnc) Halt(pane string, r engine.Result, asOfMS int64) []byte {
	return []byte("halt:" + pane)
}
func (fakeEnc) SessionFrames(seq engine.Seq, ev engine.Event, emit func(string, []byte)) {
	if r, ok := ev.(engine.Rested); ok {
		emit(string(r.Session), []byte("rested"))
	}
}

type reports struct {
	mu   sync.Mutex
	last struct {
		seq       uint64
		orders    int64
		laneDrops uint64
		halted    bool
	}
}

func (r *reports) Engine(seq uint64, orders int64, laneDrops uint64, halted bool) {
	r.mu.Lock()
	r.last.seq, r.last.orders, r.last.laneDrops, r.last.halted = seq, orders, laneDrops, halted
	r.mu.Unlock()
}

func boot(t *testing.T, degraded Publisher) (*Runner, *capture, *reports, func()) {
	t.Helper()
	direct := newCapture()
	rep := &reports{}
	if degraded == nil {
		degraded = newCapture()
	}
	r := New(Config{
		Book: engine.New(engine.Config{Depth: 8}), Direct: direct, Degraded: degraded,
		Enc: fakeEnc{}, Report: rep, Clock: func() int64 { return 0 },
	})
	ctx, cancel := context.WithCancel(context.Background())
	keyframe := make(chan time.Time)
	go r.Run(ctx, keyframe)
	return r, direct, rep, func() { cancel(); <-r.Done() }
}

func settle(r *Runner) {
	// The loop is single-threaded, so once a later command has been observed the
	// earlier ones are provably done. Polling a counter beats sleeping.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(r.cmds) == 0 {
			time.Sleep(20 * time.Millisecond)
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// ── tests ───────────────────────────────────────────────────────────────────

func TestPublishesBothLanesAndTheTraderLane(t *testing.T) {
	r, direct, _, stop := boot(t, nil)
	defer stop()

	if err := r.Submit(engine.Submit{Session: "alice", Side: engine.Buy, Price: 10240, Qty: 100}); err != nil {
		t.Fatal(err)
	}
	settle(r)

	if direct.count(TopicBookFresh) == 0 {
		t.Error("no frame on the fresh lane")
	}
	if direct.count(TopicTop) == 0 {
		t.Error("no top-of-book frame for phones")
	}
	if direct.count(TopicSession("alice")) == 0 {
		t.Error("the trader never heard about their own order")
	}
	// Session frames must go down the UNDEGRADED lane: chaos is aimed at the
	// projector's second pane, and degrading phones too would confound the
	// experiment with a second variable.
	for _, tp := range direct.topics() {
		if strings.HasPrefix(tp, "s:") {
			return
		}
	}
	t.Error("session frames were not published on the direct lane")
}

// failAfter is an Applier that behaves correctly and then reports a violation,
// which a correct engine can never be made to do. Without this seam the halt
// branch would only ever run in front of an audience.
type failAfter struct {
	real  *engine.Book
	after engine.Seq

	// Guarded: Apply runs on the loop goroutine while the test reads the count
	// from its own. Production code needs no lock here because the book has one
	// owner; a test fake observed from outside is a different situation, and
	// pretending otherwise is how a test introduces the very class of bug the
	// design exists to avoid. -race caught this on the first run.
	mu    sync.Mutex
	calls engine.Seq
}

func (f *failAfter) Seq() engine.Seq { return f.real.Seq() }

func (f *failAfter) count() engine.Seq {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func (f *failAfter) Apply(c engine.Command) engine.Result {
	f.mu.Lock()
	f.calls++
	n := f.calls
	f.mu.Unlock()
	res := f.real.Apply(c)
	if n == f.after {
		res.Report.Violations = append(res.Report.Violations, invariant.Violation{
			Kind: invariant.Conservation, Detail: "injected", Want: 4200, Got: 4050,
		})
		res.Report.Passed[invariant.Conservation] = false
	}
	return res
}

// D5: on a failing invariant the loop halts. It does not panic, and it does not
// keep matching. A panic would destroy the projector and the evidence together;
// continuing would mean projecting a book already proved wrong.
func TestHaltStopsMutationAndAnnouncesIt(t *testing.T) {
	direct := newCapture()
	rep := &reports{}
	book := &failAfter{real: engine.New(engine.Config{Depth: 8}), after: 2}
	r := New(Config{
		Book: book, Direct: direct, Degraded: newCapture(), Enc: fakeEnc{},
		Report: rep, Clock: func() int64 { return 0 },
	})
	ctx, cancel := context.WithCancel(context.Background())
	keyframe := make(chan time.Time)
	go r.Run(ctx, keyframe)
	defer func() { cancel(); <-r.Done() }()

	_ = r.Submit(engine.Submit{Session: "a", Side: engine.Buy, Price: 10240, Qty: 100})
	settle(r)
	_ = r.Submit(engine.Submit{Session: "b", Side: engine.Sell, Price: 10260, Qty: 100}) // trips it
	settle(r)

	appliedAtHalt := book.count()
	freshAtHalt := direct.count(TopicBookFresh)

	// Everything after the halt must be refused.
	for i := 0; i < 5; i++ {
		_ = r.Submit(engine.Submit{Session: "c", Side: engine.Buy, Price: 10240, Qty: 50})
	}
	settle(r)

	if direct.count(TopicHalt) == 0 {
		t.Fatal("no halt frame was published; the room would see a frozen book and no reason")
	}
	if got := book.count(); got != appliedAtHalt {
		t.Errorf("the engine kept mutating after the halt: %d applies -> %d", appliedAtHalt, got)
	}
	if direct.count(TopicBookFresh) != freshAtHalt {
		t.Error("a book frame was published after the halt; the last good book must stay retained instead")
	}
	rep.mu.Lock()
	halted := rep.last.halted
	rep.mu.Unlock()
	if !halted {
		t.Error("the halt was never reported to the telemetry path")
	}
	t.Logf("halted after %d applies; %d further commands refused", appliedAtHalt, 5)
}

// The engine must never be stalled by a full command queue: it rejects rather
// than blocking, and rejecting is visible to the trader.
func TestSubmitRejectsWhenTheQueueIsFull(t *testing.T) {
	r := New(Config{
		Book: engine.New(engine.Config{}), Direct: newCapture(), Degraded: newCapture(),
		Enc: fakeEnc{}, Clock: func() int64 { return 0 }, QueueDepth: 4,
	})
	// Deliberately NOT started: nothing is draining.
	var errs int
	for i := 0; i < 50; i++ {
		if err := r.Submit(engine.Submit{Session: "x", Side: engine.Buy, Price: 100, Qty: 1}); err != nil {
			errs++
		}
	}
	if errs == 0 {
		t.Fatal("Submit accepted 50 commands into a queue of 4 with no consumer")
	}
	t.Logf("%d of 50 rejected with ErrBusy rather than blocking", errs)
}

// A refusing degraded lane must be counted, not swallowed: those losses are the
// number act two puts on screen.
func TestDegradedLaneRefusalsAreCounted(t *testing.T) {
	refuser := newCapture()
	refuser.refuse = true
	r, _, rep, stop := boot(t, refuser)
	defer stop()

	for i := 0; i < 5; i++ {
		_ = r.Submit(engine.Submit{Session: "a", Side: engine.Buy, Price: engine.Ticks(10240 - i), Qty: 10})
	}
	settle(r)

	rep.mu.Lock()
	drops := rep.last.laneDrops
	rep.mu.Unlock()
	if drops == 0 {
		t.Fatal("the degraded lane refused every frame and none was counted")
	}
	t.Logf("counted %d refused frames on the degraded lane", drops)
}

func TestKeyframeRepublishesTheCurrentBook(t *testing.T) {
	direct := newCapture()
	r := New(Config{
		Book: engine.New(engine.Config{Depth: 8}), Direct: direct, Degraded: newCapture(),
		Enc: fakeEnc{}, Clock: func() int64 { return 0 },
	})
	ctx, cancel := context.WithCancel(context.Background())
	keyframe := make(chan time.Time)
	go r.Run(ctx, keyframe)
	defer func() { cancel(); <-r.Done() }()

	_ = r.Submit(engine.Submit{Session: "a", Side: engine.Buy, Price: 10240, Qty: 100})
	settle(r)
	before := direct.count(TopicBookFresh)

	keyframe <- time.Now()
	settle(r)

	if direct.count(TopicBookFresh) <= before {
		t.Fatal("a keyframe tick did not republish the book — a degraded pane would never snap back")
	}
}
