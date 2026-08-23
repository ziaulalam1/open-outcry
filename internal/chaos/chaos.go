// Package chaos is the ONLY place in this repository where anything is
// deliberately broken.
//
// It imports nothing from this module. Its single outward reference is a
// `func(topic string, frame []byte) bool` — the same signature the hub's
// Publish has — so it is structurally a drop-in decorator on the outbound path
// and cannot name engine.Book, engine.Command, or any other domain type. That
// is what makes "the chaos layer cannot reach the engine" a compile-time fact
// rather than a claim, and it is why act two's core assertion survives: whatever
// happens to delivery, nothing in here can have touched the book.
package chaos

import (
	"context"
	"sync"
	"time"
)

// Downstream is what a Line forwards to. In production it is hub.Publish.
type Downstream func(topic string, frame []byte) bool

type Policy struct {
	Delay     time.Duration // how far behind the degraded lane runs
	DropEvery int           // drop 1 in N frames; 0 disables dropping
	Armed     bool
}

type item struct {
	topic   string
	frame   []byte
	readyAt time.Time
}

// Line is a bounded delay ring with a single timer.
//
// It is NOT `time.Sleep(d)` in the forwarding loop, and that distinction is the
// whole implementation. A sleep in the loop does not inject latency, it caps
// THROUGHPUT: at a mean 500ms delay a sleeping loop passes about two frames a
// second against an inbound rate of fifteen, the ring fills in under twenty
// seconds, and from then on it discards most of what arrives. On stage the
// degraded pane would not rot gracefully — it would go nearly static about
// twenty seconds into act two, stop receiving the keyframes the "snap back"
// payoff depends on, and attribute the losses to the wrong counter.
//
// It is also NOT `go func(){ time.Sleep(d); send() }()` per frame, which is
// worse: unbounded goroutines AND reordered frames.
//
// One goroutine, one timer, FIFO by construction.
type Line struct {
	in     chan item
	ctrl   chan Policy
	out    Downstream
	report func(dropped uint64, delayMS int, armed bool)
	done   chan struct{}
	ring   int

	// Policy is read by callers of Snapshot for tests only; the Run goroutine
	// owns the live copy and receives changes as messages.
	mu     sync.Mutex
	latest Policy
}

type Config struct {
	Out    Downstream
	Report func(dropped uint64, delayMS int, armed bool)
	Ring   int
}

func New(cfg Config) *Line {
	if cfg.Ring <= 0 {
		cfg.Ring = 512
	}
	return &Line{
		in:     make(chan item, cfg.Ring),
		ctrl:   make(chan Policy, 8),
		out:    cfg.Out,
		report: cfg.Report,
		done:   make(chan struct{}),
		ring:   cfg.Ring,
	}
}

func (l *Line) Done() <-chan struct{} { return l.done }

// Publish implements loop.Publisher. Never blocks the engine; returns false if
// the ring is full, letting the CALLER count its own loss rather than reaching
// into a counter this package owns.
func (l *Line) Publish(topic string, frame []byte) bool {
	select {
	case l.in <- item{topic: topic, frame: frame}:
		return true
	case <-l.done:
		return false
	default:
		return false
	}
}

// Configure changes the policy. Presenter toggles arrive as MESSAGES, not as an
// atomic flag: an atomic would put mutable shared state on the demo's critical
// path, in the one package whose isolation the entire argument depends on.
func (l *Line) Configure(p Policy) {
	l.mu.Lock()
	l.latest = p
	l.mu.Unlock()
	select {
	case l.ctrl <- p:
	case <-l.done:
	}
}

// Snapshot reports the most recently requested policy. Used by the HTTP handler
// to echo state back to the presenter; never read by Run.
func (l *Line) Snapshot() Policy {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.latest
}

// Run owns the ring, the timer, the policy and the drop counter.
func (l *Line) Run(ctx context.Context) {
	defer close(l.done)

	var (
		pol     Policy
		pending []item
		seen    uint64
		dropped uint64
	)

	timer := time.NewTimer(time.Hour)
	if !timer.Stop() {
		<-timer.C
	}
	defer timer.Stop()

	reportTick := time.NewTicker(200 * time.Millisecond)
	defer reportTick.Stop()

	arm := func() {
		if len(pending) == 0 {
			return
		}
		d := time.Until(pending[0].readyAt)
		if d < 0 {
			d = 0
		}
		timer.Reset(d)
	}

	flush := func() {
		now := time.Now()
		i := 0
		for ; i < len(pending); i++ {
			if pending[i].readyAt.After(now) {
				break
			}
			l.out(pending[i].topic, pending[i].frame)
		}
		if i > 0 {
			pending = append(pending[:0], pending[i:]...)
		}
		arm()
	}

	for {
		select {
		case <-ctx.Done():
			return

		case p := <-l.ctrl:
			pol = p
			if !pol.Armed {
				// Switching chaos off releases everything still in flight at
				// once. The client discards frames whose seq has already been
				// superseded, so this is a clean snap back rather than a burst
				// of stale books rendering in reverse.
				for _, it := range pending {
					l.out(it.topic, it.frame)
				}
				pending = pending[:0]
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
			}

		case it := <-l.in:
			if !pol.Armed {
				l.out(it.topic, it.frame) // synchronous passthrough when disarmed
				continue
			}
			seen++
			if pol.DropEvery > 0 && seen%uint64(pol.DropEvery) == 0 {
				dropped++
				continue
			}
			if len(pending) >= l.ring {
				// The ring is full even after dropping: shed the OLDEST, because
				// frames are self-contained snapshots and the newest is strictly
				// more useful than the one it replaces.
				dropped++
				pending = pending[1:]
			}
			it.readyAt = time.Now().Add(pol.Delay)
			pending = append(pending, it)
			if len(pending) == 1 {
				arm()
			}

		case <-timer.C:
			flush()

		case <-reportTick.C:
			if l.report != nil {
				l.report(dropped, int(pol.Delay/time.Millisecond), pol.Armed)
			}
		}
	}
}
