// Package hub fans frames out to connected clients.
//
// It imports NOTHING from this module. It cannot name engine.Book, engine.Order
// or any other domain type, which is what makes "the transport cannot reach the
// engine" a fact the compiler enforces rather than a promise in a README.
//
// One goroutine owns the client registry and every counter. Nothing else reads
// them — not even to render them. The numbers reach the projector the same way
// the book does: as a message, composed on the goroutine that owns them.
package hub

import (
	"context"
	"runtime"
	"time"
)

// SendBuffer is how many frames a slow client may fall behind before its frames
// start being dropped.
//
// 64 is roughly two seconds at demo rates. The number matters less than the
// policy: this buffer is bounded, and when it is full the hub drops rather than
// blocks. One slow phone must never stall the room, and a buffer without a
// bound is just a slower way to run out of memory.
const SendBuffer = 64

// Stats is a plain value, composed by the hub on the hub's own goroutine.
type Stats struct {
	Clients      int
	Orders       int64
	Backpressure uint64
	ChaosDropped uint64
	ChaosDelayMS int
	Goroutines   int
	EngineSeq    uint64
	Split        bool
	Halted       bool
}

// StatsEncoder is injected. The hub owns the counters but must not import wire,
// so the encoding function is handed in and called on the hub's goroutine.
//
// This closes the hole that every draft of this design had: the goroutine that
// owns the numbers could not marshal them, and the package that could marshal
// them was not allowed to read them. Passing a function solves it without a
// shared mutable value and without an atomic — an atomic would make the counters
// look safely readable from anywhere, and the next person would read them from
// an HTTP handler and watch the projector's numbers tear mid-broadcast.
type StatsEncoder func(Stats) []byte

type engineReport struct {
	seq       uint64
	orders    int64
	laneDrops uint64
	halted    bool
}

type chaosReport struct {
	dropped uint64
	delayMS int
	armed   bool
}

type Hub struct {
	pub      chan frame
	reg      chan *Client
	unreg    chan *Client
	fromEng  chan engineReport
	fromCh   chan chaosReport
	encode   StatsEncoder
	interval time.Duration
	done     chan struct{}

	// Owned exclusively by Run's goroutine.
	clients      map[*Client]struct{}
	retained     map[string][]byte
	backpressure uint64
	eng          engineReport
	chaos        chaosReport
}

type frame struct {
	topic string
	bytes []byte
}

func New(encode StatsEncoder, interval time.Duration) *Hub {
	if interval <= 0 {
		interval = 250 * time.Millisecond
	}
	return &Hub{
		pub:      make(chan frame, 256),
		reg:      make(chan *Client),
		unreg:    make(chan *Client, 64),
		fromEng:  make(chan engineReport, 256),
		fromCh:   make(chan chaosReport, 64),
		encode:   encode,
		interval: interval,
		done:     make(chan struct{}),
		clients:  make(map[*Client]struct{}),
		retained: make(map[string][]byte),
	}
}

func (h *Hub) Done() <-chan struct{} { return h.done }

// Publish implements loop.Publisher. Never blocks; returns false if it shed.
func (h *Hub) Publish(topic string, b []byte) bool {
	select {
	case h.pub <- frame{topic, b}:
		return true
	case <-h.done:
		return false
	default:
		return false
	}
}

// Engine implements loop.Reporter — counters travel in, never out.
func (h *Hub) Engine(seq uint64, orders int64, laneDrops uint64, halted bool) {
	select {
	case h.fromEng <- engineReport{seq, orders, laneDrops, halted}:
	case <-h.done:
	default: // telemetry is lossy by design; the next one is 4ms away
	}
}

// Chaos is the same one-way push, from the chaos line.
func (h *Hub) Chaos(dropped uint64, delayMS int, armed bool) {
	select {
	case h.fromCh <- chaosReport{dropped, delayMS, armed}:
	case <-h.done:
	default:
	}
}

// Run owns the registry and every counter for its lifetime.
//
// The load-bearing liveness property: this loop NEVER blocks. Every send is
// select/default, every receive is in a select, it holds no locks, does no I/O
// and never calls into the engine. That is what makes `unreg <- c` from a read
// pump complete in bounded time, which is what makes read pumps unable to leak.
// Requirement 3's drop policy and requirement 7's no-leak guarantee are the same
// property seen from two ends.
func (h *Hub) Run(ctx context.Context) {
	defer close(h.done)
	tick := time.NewTicker(h.interval)
	defer tick.Stop() // a ticker is a runtime timer, not a goroutine

	for {
		select {
		case <-ctx.Done():
			h.shutdown()
			return

		case c := <-h.reg:
			h.clients[c] = struct{}{}
			// Replay retained frames IMMEDIATELY, from this goroutine, before any
			// subsequent broadcast can reach the new client. Registering first and
			// filling from the same goroutine is what makes the join gapless; the
			// naive order (fetch a snapshot, then register) loses every event in
			// between, and the browser renders a book with holes — which on a
			// projector looks exactly like an engine bug.
			for _, topic := range c.topics {
				if b, ok := h.retained[topic]; ok {
					h.trySend(c, b)
				}
			}

		case c := <-h.unreg:
			h.remove(c)

		case f := <-h.pub:
			h.deliver(f.topic, f.bytes)

		case r := <-h.fromEng:
			h.eng = r

		case r := <-h.fromCh:
			h.chaos = r

		case <-tick.C:
			h.deliver(topicStats, h.encode(Stats{
				Clients:      len(h.clients),
				Orders:       h.eng.orders,
				Backpressure: h.backpressure,
				ChaosDropped: h.chaos.dropped + h.eng.laneDrops,
				ChaosDelayMS: h.chaos.delayMS,
				Goroutines:   runtime.NumGoroutine(),
				EngineSeq:    h.eng.seq,
				Split:        h.chaos.armed,
				Halted:       h.eng.halted,
			}))
		}
	}
}

const topicStats = "stats"

func (h *Hub) deliver(topic string, b []byte) {
	h.retained[topic] = b
	for c := range h.clients {
		if c.subscribed(topic) {
			h.trySend(c, b)
		}
	}
}

// trySend is THE drop policy, and it is three lines because it should be.
//
// The counter needs neither a mutex nor an atomic: the only goroutine that can
// observe a full buffer is the only goroutine that fills it, so the count is
// already at its owner. A mutex here would not be a bug, it would be evidence
// that ownership had been drawn in the wrong place — and its presence would
// invite the next one.
func (h *Hub) trySend(c *Client, b []byte) {
	select {
	case c.send <- b:
	default:
		c.dropped++
		h.backpressure++
	}
}

// remove is idempotent, and the delete happens BEFORE the close.
//
// This is the resolution of the classic chat-server inversion. The read pump is
// the only goroutine that can notice a disconnect, but the hub is the only
// goroutine that sends on c.send — so detection and closure are split. The read
// pump reports; the hub deregisters and only then closes. After the delete no
// code path in this file can reach that channel, which is what makes the close
// provably safe rather than merely usually safe.
func (h *Hub) remove(c *Client) {
	if _, ok := h.clients[c]; !ok {
		return // an unregister can arrive twice under churn
	}
	delete(h.clients, c)
	close(c.send)
}

func (h *Hub) shutdown() {
	for c := range h.clients {
		delete(h.clients, c)
		close(c.send)
	}
}
