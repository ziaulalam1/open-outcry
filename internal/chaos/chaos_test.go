package chaos

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"testing"
	"time"

	"go.uber.org/goleak"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

type sink struct {
	mu   sync.Mutex
	got  []string
	seen chan struct{}
}

func newSink() *sink { return &sink{seen: make(chan struct{}, 4096)} }

func (s *sink) out(topic string, b []byte) bool {
	s.mu.Lock()
	s.got = append(s.got, string(b))
	s.mu.Unlock()
	select {
	case s.seen <- struct{}{}:
	default:
	}
	return true
}

func (s *sink) snapshot() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.got...)
}

func (s *sink) waitFor(n int, d time.Duration) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		got := len(s.got)
		s.mu.Unlock()
		if got >= n {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return false
}

func start(t *testing.T, s *sink) (*Line, func()) {
	t.Helper()
	l := New(Config{Out: s.out, Ring: 64})
	ctx, cancel := context.WithCancel(context.Background())
	go l.Run(ctx)
	return l, func() { cancel(); <-l.Done() }
}

func TestPassthroughWhenDisarmed(t *testing.T) {
	s := newSink()
	l, stop := start(t, s)
	defer stop()

	for i := 0; i < 20; i++ {
		l.Publish("t", []byte(fmt.Sprint(i)))
	}
	if !s.waitFor(20, time.Second) {
		t.Fatalf("disarmed line delivered %d of 20", len(s.snapshot()))
	}
}

// FIFO is the property that makes the degraded pane show a book that WAS true,
// rather than one that never existed. A per-frame `go func(){ sleep; send }()`
// would deliver out of order and quietly break that.
func TestDelayLinePreservesOrder(t *testing.T) {
	s := newSink()
	l, stop := start(t, s)
	defer stop()

	l.Configure(Policy{Armed: true, Delay: 60 * time.Millisecond})
	for i := 0; i < 30; i++ {
		l.Publish("t", []byte(fmt.Sprint(i)))
		time.Sleep(time.Millisecond)
	}
	if !s.waitFor(30, 3*time.Second) {
		t.Fatalf("delivered %d of 30", len(s.snapshot()))
	}
	for i, v := range s.snapshot() {
		if v != fmt.Sprint(i) {
			t.Fatalf("frame %d out of order: got %q", i, v)
		}
	}
}

func TestDelayIsActuallyApplied(t *testing.T) {
	s := newSink()
	l, stop := start(t, s)
	defer stop()

	const delay = 150 * time.Millisecond
	l.Configure(Policy{Armed: true, Delay: delay})
	time.Sleep(10 * time.Millisecond)

	sent := time.Now()
	l.Publish("t", []byte("x"))
	if !s.waitFor(1, 2*time.Second) {
		t.Fatal("frame never arrived")
	}
	if elapsed := time.Since(sent); elapsed < delay-30*time.Millisecond {
		t.Fatalf("frame arrived after %s, expected at least ~%s", elapsed, delay)
	}
}

func TestDropEveryNDropsAndCounts(t *testing.T) {
	s := newSink()
	var (
		mu      sync.Mutex
		dropped uint64
	)
	l := New(Config{Out: s.out, Ring: 256, Report: func(d uint64, _ int, _ bool) {
		mu.Lock()
		dropped = d
		mu.Unlock()
	}})
	ctx, cancel := context.WithCancel(context.Background())
	go l.Run(ctx)
	defer func() { cancel(); <-l.Done() }()

	l.Configure(Policy{Armed: true, DropEvery: 3})
	time.Sleep(10 * time.Millisecond)
	for i := 0; i < 30; i++ {
		l.Publish("t", []byte(fmt.Sprint(i)))
	}
	time.Sleep(500 * time.Millisecond)

	got := len(s.snapshot())
	if got == 30 || got == 0 {
		t.Fatalf("delivered %d of 30; expected roughly two thirds", got)
	}
	mu.Lock()
	d := dropped
	mu.Unlock()
	if d == 0 {
		t.Fatal("drops were not counted or not reported")
	}
	t.Logf("delivered %d of 30, reported %d drops", got, d)
}

// The claim this test exists to defend: injecting latency must not spawn a
// goroutine per frame. One goroutine, one timer, however hard it is pushed.
func TestDelayLineIsBoundedUnderBurst(t *testing.T) {
	s := newSink()
	l, stop := start(t, s)
	defer stop()

	l.Configure(Policy{Armed: true, Delay: 400 * time.Millisecond})
	time.Sleep(10 * time.Millisecond)

	runtime.GC()
	before := runtime.NumGoroutine()
	for i := 0; i < 10_000; i++ {
		l.Publish("t", []byte(fmt.Sprint(i)))
	}
	after := runtime.NumGoroutine()

	if after > before+2 {
		t.Fatalf("goroutines grew from %d to %d under a 10k burst — the delay line is spawning per frame",
			before, after)
	}
	t.Logf("10,000 frames: goroutines %d -> %d", before, after)
}

// Switching chaos off must release what is still in flight, or the pane stays
// wrong until the next order happens to arrive.
func TestDisarmReleasesPendingFrames(t *testing.T) {
	s := newSink()
	l, stop := start(t, s)
	defer stop()

	l.Configure(Policy{Armed: true, Delay: 10 * time.Second})
	time.Sleep(10 * time.Millisecond)
	for i := 0; i < 5; i++ {
		l.Publish("t", []byte(fmt.Sprint(i)))
	}
	time.Sleep(50 * time.Millisecond)
	if n := len(s.snapshot()); n != 0 {
		t.Fatalf("%d frames escaped a 10s delay", n)
	}

	l.Configure(Policy{Armed: false})
	if !s.waitFor(5, time.Second) {
		t.Fatalf("disarming released only %d of 5 pending frames", len(s.snapshot()))
	}
}

func TestPublishNeverBlocksWhenRingIsFull(t *testing.T) {
	// No Run goroutine at all: nothing is draining the input channel.
	l := New(Config{Out: func(string, []byte) bool { return true }, Ring: 8})

	done := make(chan int, 1)
	go func() {
		accepted := 0
		for i := 0; i < 1000; i++ {
			if l.Publish("t", []byte("x")) {
				accepted++
			}
		}
		done <- accepted
	}()

	select {
	case accepted := <-done:
		if accepted >= 1000 {
			t.Fatal("everything was accepted with nothing draining; the ring is not bounded")
		}
		t.Logf("accepted %d of 1000 with no consumer, refused the rest without blocking", accepted)
	case <-time.After(2 * time.Second):
		t.Fatal("Publish blocked — the engine can be stalled by the chaos layer")
	}
}
