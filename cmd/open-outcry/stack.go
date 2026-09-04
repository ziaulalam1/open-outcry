package main

import (
	"context"
	"sync"
	"time"

	"github.com/ziaulalam1/open-outcry/internal/chaos"
	"github.com/ziaulalam1/open-outcry/internal/engine"
	"github.com/ziaulalam1/open-outcry/internal/hub"
	"github.com/ziaulalam1/open-outcry/internal/loop"
	"github.com/ziaulalam1/open-outcry/internal/seed"
	"github.com/ziaulalam1/open-outcry/internal/wire"
)

// stack is the whole running system, assembled.
//
// It is a type rather than fifty lines inline in main() for exactly one reason:
// the concurrency claims this project makes — no leaks, bounded goroutines, a
// drop counter that actually moves — are only worth anything if they are tested
// against the REAL wiring. A test that assembles a different stack proves
// something about the test.
type stack struct {
	Hub   *hub.Hub
	Loop  *loop.Runner
	Chaos *chaos.Line
	Enc   wire.Encoder
	Book  *engine.Book
	keyfr *time.Ticker

	// conns counts live WebSocket connections. It lives on the stack rather than
	// at package scope so a test can boot two independent servers without their
	// shutdowns waiting on each other.
	conns   sync.WaitGroup
	infra   sync.WaitGroup
	stopEng context.CancelFunc
}

type stackConfig struct {
	StatsInterval time.Duration
	KeyframeEvery time.Duration
	Seed          bool
}

func newStack(cfg stackConfig) *stack {
	if cfg.StatsInterval <= 0 {
		cfg.StatsInterval = 250 * time.Millisecond
	}
	if cfg.KeyframeEvery <= 0 {
		cfg.KeyframeEvery = 500 * time.Millisecond
	}

	enc := wire.New()
	start := time.Now()
	clock := func() int64 { return time.Since(start).Milliseconds() }

	h := hub.New(func(s hub.Stats) []byte {
		return enc.Stats(wire.StatsInput{
			Clients: s.Clients, Orders: s.Orders, Backpressure: s.Backpressure,
			ChaosDropped: s.ChaosDropped, ChaosDelayMS: s.ChaosDelayMS,
			Goroutines: s.Goroutines, EngineSeq: s.EngineSeq, Split: s.Split,
		})
	}, cfg.StatsInterval)

	line := chaos.New(chaos.Config{Out: h.Publish, Report: h.Chaos})
	book := engine.New(engine.Config{Depth: 8})
	runner := loop.New(loop.Config{
		Book: book, Direct: h, Degraded: line, Enc: enc, Report: h, Clock: clock,
	})

	s := &stack{
		Hub: h, Loop: runner, Chaos: line, Enc: enc, Book: book,
		keyfr: time.NewTicker(cfg.KeyframeEvery),
	}

	if cfg.Seed {
		// Through the front door, as ordinary commands. There is no constructor
		// that injects orders into the book.
		for _, c := range seed.Baseline() {
			_ = runner.Submit(c)
		}
	}
	return s
}

// start launches the three infrastructure goroutines. The engine gets its OWN
// context, cancelled by hand in stop(), because this shutdown depends on
// goroutines dying in a specific order and a single shared cancel erases that.
func (s *stack) start(ctx context.Context) {
	engCtx, stopEng := context.WithCancel(context.Background())
	s.stopEng = stopEng

	s.infra.Add(3)
	go func() { defer s.infra.Done(); s.Loop.Run(engCtx, s.keyfr.C) }()
	go func() { defer s.infra.Done(); s.Chaos.Run(ctx) }()
	go func() { defer s.infra.Done(); s.Hub.Run(ctx) }()
}

// waitConns blocks until every hijacked connection has torn down, or the budget
// expires. net/http's Shutdown explicitly does NOT wait for hijacked
// connections, so without this the server "shuts down" instantly and leaks every
// socket.
func (s *stack) waitConns(d time.Duration) bool {
	done := make(chan struct{})
	go func() { s.conns.Wait(); close(done) }()
	select {
	case <-done:
		return true
	case <-time.After(d):
		return false
	}
}

// stop tears the stack down in data-flow order. Callers must have already
// cancelled the context passed to start and waited for connections to drain:
// the fan-out layer owns the sockets, and closing sockets is the only thing
// that unblocks the read pumps feeding the engine.
func (s *stack) stop() {
	s.keyfr.Stop()
	s.stopEng()
	<-s.Loop.Done()
	<-s.Hub.Done()
	<-s.Chaos.Done()
	s.infra.Wait()
}
