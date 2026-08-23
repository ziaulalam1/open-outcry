// Package loop owns the order book.
//
// Exactly one goroutine in the process holds the *engine.Book pointer, and it is
// the one started by Runner.Run. Commands arrive as values on a channel and
// results leave as bytes. No other goroutine can reach book state, which is why
// "no data races by construction" is a claim about ownership rather than about
// locks — there are no locks anywhere in this repository.
package loop

import (
	"context"
	"errors"
	"time"

	"github.com/ziaulalam1/open-outcry/internal/engine"
)

// Topics. Frame TYPE is part of the topic on purpose: the hub retains one frame
// per topic for gapless joins, so publishing a halt onto the book topic would
// evict the book and leave a late joiner with a banner over an empty ladder.
const (
	TopicBookFresh = "book:fresh" // the control: undegraded
	TopicBookStale = "book:stale" // the experiment: behind the chaos decorator
	TopicStats     = "stats"
	TopicHalt      = "halt"
	TopicTop       = "top" // best bid/ask, for phones
)

func TopicSession(session string) string { return "s:" + session }

// ErrBusy means the command channel was full.
//
// The outbound drop policy is NEVER applied inbound. A trader seeing their order
// accepted when it never reached the book is a correctness bug wearing a
// delivery bug's clothes, and it would destroy the one claim act two exists to
// make. Outbound: drop and count. Inbound: reject and say so.
var ErrBusy = errors.New("engine busy")

// Publisher is declared consumer-side so this package never imports the hub.
// It returns false when the frame was dropped rather than delivered, letting
// each caller count its own losses instead of reading someone else's counter.
type Publisher interface {
	Publish(topic string, frame []byte) bool
}

// Encoder is declared consumer-side so this package never imports wire, which
// is how "encoding/json appears in exactly one package" stays true.
//
// SessionFrames takes a callback rather than returning a slice of some shared
// struct, which would have forced a type to live in a package both this one and
// wire could see, and dragged the wire format back across the boundary.
type Encoder interface {
	Book(pane string, r engine.Result, actor engine.SessionID, asOfMS int64, halted bool) []byte
	Top(r engine.Result, asOfMS int64) []byte
	Halt(pane string, r engine.Result, asOfMS int64) []byte
	SessionFrames(seq engine.Seq, ev engine.Event, emit func(session string, frame []byte))
}

// Applier is the engine, named consumer-side.
//
// *engine.Book satisfies it. Declaring it here rather than depending on the
// concrete type is not ceremony: the halt path is the one branch that a correct
// engine can never exercise, so without a seam there is no way to test what the
// room actually sees when an invariant fails. A branch that only runs on stage
// is a branch that has never run.
type Applier interface {
	Apply(engine.Command) engine.Result
	Seq() engine.Seq
}

// Reporter carries the loop's own counters OUT to whoever renders them. It is a
// one-way push, never a read: nothing outside this goroutine may look at these
// values, so they travel as a message like everything else.
type Reporter interface {
	Engine(seq uint64, orders int64, laneDrops uint64, halted bool)
}

// Clock returns milliseconds since the process started.
//
// Injected rather than read from time.Now(), because AsOfMS must be stamped HERE
// — upstream of the chaos decorator — or the chaos layer could hide its own
// latency by restamping frames on the way past. It also lets tests advance time
// without sleeping.
type Clock func() int64

type Config struct {
	Book       Applier
	Direct     Publisher // undegraded lane
	Degraded   Publisher // the same frames, behind chaos
	Enc        Encoder
	Report     Reporter
	Clock      Clock
	QueueDepth int
}

type Runner struct {
	book     Applier
	cmds     chan engine.Command
	direct   Publisher
	degraded Publisher
	enc      Encoder
	report   Reporter
	clock    Clock
	done     chan struct{}

	// Everything below is touched ONLY by the Run goroutine.
	halted    bool
	orders    int64
	laneDrops uint64
	lastGood  engine.Result
	haveGood  bool
}

func New(cfg Config) *Runner {
	if cfg.QueueDepth <= 0 {
		cfg.QueueDepth = 1024
	}
	return &Runner{
		book:     cfg.Book,
		cmds:     make(chan engine.Command, cfg.QueueDepth),
		direct:   cfg.Direct,
		degraded: cfg.Degraded,
		enc:      cfg.Enc,
		report:   cfg.Report,
		clock:    cfg.Clock,
		done:     make(chan struct{}),
	}
}

func (r *Runner) Done() <-chan struct{} { return r.done }

// Submit hands a command to the loop without blocking.
//
// Many senders (one per connection), one receiver. The channel is never closed:
// with N senders there is no safe closer, so the receiver exits on context
// instead and senders guard on Done.
func (r *Runner) Submit(c engine.Command) error {
	select {
	case r.cmds <- c:
		return nil
	case <-r.done:
		return ErrBusy
	default:
		return ErrBusy
	}
}

// Run is the only goroutine that touches the book.
// keyframe is a <-chan time.Time so main can hand it a ticker's channel
// directly. Converting a ticker into a struct{} channel would cost a goroutine
// whose only job is forwarding, and the goroutine census is a number rendered on
// the projector — every one of them has to earn its place.
func (r *Runner) Run(ctx context.Context, keyframe <-chan time.Time) {
	defer close(r.done)
	for {
		select {
		case <-ctx.Done():
			return
		case c := <-r.cmds:
			r.apply(c)
		case <-keyframe:
			// Republishing the current book periodically is what lets a degraded
			// pane "rot and then snap back" when chaos is switched off, instead of
			// staying wrong until the next order happens to arrive.
			r.republish()
		}
	}
}

func (r *Runner) apply(c engine.Command) {
	if r.halted {
		// Quarantined. The book is not mutated again, and the last known good
		// snapshot stays retained on its topic so the room still sees a book.
		return
	}

	res := r.book.Apply(c)
	now := r.clock()

	if !res.Report.OK() {
		// D5: halt, do not panic and do not log-and-continue.
		//
		// A real venue halts the symbol on an integrity fault. A panic would
		// destroy the projector and the forensic evidence in the same instant,
		// in front of the room. Continuing would mean projecting a book we have
		// just proved is wrong, which is the exact opposite of act two's claim.
		r.halted = true
		frame := r.enc.Halt("fresh", res, now)
		r.direct.Publish(TopicHalt, frame)
		if !r.degraded.Publish(TopicHalt, frame) {
			r.laneDrops++
		}
		r.pushReport(uint64(res.Seq))
		return
	}

	r.orders++
	r.lastGood, r.haveGood = res, true

	actor := actorOf(c)
	r.publishBook(res, actor, now)
	r.direct.Publish(TopicTop, r.enc.Top(res, now))

	for _, ev := range res.Events {
		r.enc.SessionFrames(res.Seq, ev, func(session string, frame []byte) {
			if session == "" {
				return
			}
			// Session frames go down the UNDEGRADED lane. The chaos is aimed at
			// the projector's second pane, not at the traders: degrading the
			// phones would confound the experiment with a second variable.
			r.direct.Publish(TopicSession(session), frame)
		})
	}

	r.pushReport(uint64(res.Seq))
}

func (r *Runner) republish() {
	if !r.haveGood || r.halted {
		return
	}
	r.publishBook(r.lastGood, "", r.clock())
}

func (r *Runner) publishBook(res engine.Result, actor engine.SessionID, now int64) {
	// Encoded ONCE per lane. Both lanes carry the same book at the same AsOfMS;
	// the only difference between them is whether the frame arrives.
	r.direct.Publish(TopicBookFresh, r.enc.Book("fresh", res, actor, now, r.halted))
	if !r.degraded.Publish(TopicBookStale, r.enc.Book("stale", res, actor, now, r.halted)) {
		r.laneDrops++
	}
}

func (r *Runner) pushReport(seq uint64) {
	if r.report != nil {
		r.report.Engine(seq, r.orders, r.laneDrops, r.halted)
	}
}

func actorOf(c engine.Command) engine.SessionID {
	switch x := c.(type) {
	case engine.Submit:
		return x.Session
	case engine.Cancel:
		return x.Session
	}
	return ""
}
