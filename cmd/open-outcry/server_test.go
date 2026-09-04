package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"net"
	"syscall"

	"github.com/gorilla/websocket"
	"github.com/ziaulalam1/open-outcry/internal/chaos"
	"github.com/ziaulalam1/open-outcry/internal/wire"
	"go.uber.org/goleak"
)

// These tests run against the REAL wiring — the same newStack() and routes()
// main() uses. A concurrency claim verified against a stack assembled specially
// for the test is a claim about the test.

func boot(t *testing.T, cfg stackConfig) (base string, st *stack, done func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	st = newStack(cfg)
	st.start(ctx)

	srv := httptest.NewServer(routes(st, 8080))

	return "ws" + strings.TrimPrefix(srv.URL, "http"), st, func() {
		cancel() // hub and chaos observe this; every client's send channel closes
		if !st.waitConns(5 * time.Second) {
			t.Error("connections did not drain within budget")
		}
		st.stop()
		srv.Close()
	}
}

func dial(t *testing.T, base, query string) *websocket.Conn {
	t.Helper()
	c, _, err := websocket.DefaultDialer.Dial(base+"/ws?"+query, http.Header{})
	if err != nil {
		t.Fatalf("dial %s: %v", query, err)
	}
	return c
}

// dialConstrained opens a connection whose kernel receive buffer is tiny.
//
// This exists because loopback lies. Linux auto-tunes socket buffers into the
// megabytes, so a client that never reads still absorbs hundreds of kilobytes
// before anything backs up — and the drop policy, which is the single mechanism
// act two rests on, never fires in a test on the same machine. A phone on venue
// wifi has no such luxury. Shrinking SO_RCVBUF to the kernel minimum reproduces
// the real condition in a second rather than a minute, and reproduces it
// deterministically.
func dialConstrained(t *testing.T, base, query string) *websocket.Conn {
	t.Helper()
	d := websocket.Dialer{
		HandshakeTimeout: 5 * time.Second,
		NetDial: func(network, addr string) (net.Conn, error) {
			nd := &net.Dialer{
				Control: func(_, _ string, c syscall.RawConn) error {
					var serr error
					if err := c.Control(func(fd uintptr) {
						serr = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_RCVBUF, 2048)
					}); err != nil {
						return err
					}
					return serr
				},
			}
			return nd.Dial(network, addr)
		},
	}
	c, _, err := d.Dial(base+"/ws?"+query, http.Header{})
	if err != nil {
		t.Fatalf("dial %s: %v", query, err)
	}
	return c
}

// drain reads until the connection closes, so a client behaves like a real one.
func drain(c *websocket.Conn, into func([]byte)) {
	for {
		_, b, err := c.ReadMessage()
		if err != nil {
			return
		}
		if into != nil {
			into(b)
		}
	}
}

// ── GATE 1: no leaks under client churn ─────────────────────────────────────

func TestNoLeak_ClientChurn(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())

	base, st, done := boot(t, stackConfig{Seed: true, StatsInterval: 20 * time.Millisecond, KeyframeEvery: 20 * time.Millisecond})
	defer done()

	// A steady reader, so there is real broadcast traffic to race the churn
	// against rather than a quiet server.
	steady := dial(t, base, "feed=fresh")
	go drain(steady, nil)

	const rounds = 200
	var wg sync.WaitGroup
	for i := 0; i < rounds; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			c, _, err := websocket.DefaultDialer.Dial(
				fmt.Sprintf("%s/ws?session=churn-%d", base, i), http.Header{})
			if err != nil {
				return
			}
			// Half read, half do not: both paths through the teardown matter.
			if i%2 == 0 {
				go drain(c, nil)
			}
			_ = c.WriteMessage(websocket.TextMessage,
				wire.SubmitCmd("buy", 10240, 100).Encode())
			time.Sleep(time.Duration(i%17) * time.Millisecond)
			_ = c.Close()
		}(i)
		if i%25 == 0 {
			time.Sleep(2 * time.Millisecond) // keep the dial rate sane on CI
		}
	}
	wg.Wait()
	_ = steady.Close()

	// Give read pumps time to notice their closed sockets and deregister.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if st.waitConns(100 * time.Millisecond) {
			break
		}
	}
	t.Logf("%d connect/disconnect cycles completed", rounds)
}

// ── GATE 2: no leaks on shutdown with 50 live clients ───────────────────────

func TestNoLeak_ServerShutdown(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())

	base, _, done := boot(t, stackConfig{Seed: true, StatsInterval: 25 * time.Millisecond})

	conns := make([]*websocket.Conn, 0, 50)
	for i := 0; i < 50; i++ {
		c := dial(t, base, fmt.Sprintf("session=live-%d", i))
		go drain(c, nil)
		conns = append(conns, c)
	}
	time.Sleep(200 * time.Millisecond) // let them all register and receive

	// Shut down with every one of them still connected. This is the case
	// net/http's Shutdown does NOT handle: it neither closes nor waits for
	// hijacked connections, so a server that only calls Shutdown returns
	// instantly and leaks all fifty.
	done()

	for _, c := range conns {
		_ = c.Close()
	}
	t.Log("50 live connections at shutdown, all drained")
}

// ── GATE 3: no leaks from a client that never reads ─────────────────────────

func TestNoLeak_BlackholedClient(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())

	base, st, done := boot(t, stackConfig{Seed: true, StatsInterval: 10 * time.Millisecond, KeyframeEvery: 10 * time.Millisecond})
	defer done()

	// Connects, subscribes to the busiest topic, and never calls ReadMessage.
	// Its receive buffer fills, the server's writes to it start failing, and its
	// send channel overflows — which must cost that client frames and cost the
	// room nothing.
	black := dial(t, base, "feed=fresh")

	// Traffic, so there is something for it to fail to read.
	trader := dial(t, base, "session=busy")
	go drain(trader, nil)
	for i := 0; i < 400; i++ {
		_ = trader.WriteMessage(websocket.TextMessage,
			wire.SubmitCmd("buy", int64(10200+i%40), 50).Encode())
	}
	time.Sleep(700 * time.Millisecond)

	_ = black.Close()
	_ = trader.Close()
	st.waitConns(3 * time.Second)
	t.Log("blackholed client torn down without leaking")
}

// ── GATE 4: the goroutine census returns to baseline on disconnect ──────────

func TestGoroutineCensusReturnsToBaseline(t *testing.T) {
	base, st, done := boot(t, stackConfig{Seed: true, StatsInterval: 50 * time.Millisecond})
	defer done()

	settle := func() { time.Sleep(300 * time.Millisecond); runtime.GC() }
	settle()
	baseline := runtime.NumGoroutine()

	const n = 12
	conns := make([]*websocket.Conn, 0, n)
	for i := 0; i < n; i++ {
		c := dial(t, base, fmt.Sprintf("session=census-%d", i))
		go drain(c, nil)
		conns = append(conns, c)
	}
	settle()
	withClients := runtime.NumGoroutine()

	// Two goroutines per connection: the handler goroutine becomes the read pump
	// and one write pump is spawned. The +n is this test's own drain goroutines.
	// Asserting a RANGE rather than an exact number, because net/http keeps its
	// own per-connection bookkeeping that is not ours to predict.
	perConn := float64(withClients-baseline) / float64(n)
	if perConn < 2.0 || perConn > 4.5 {
		t.Errorf("goroutines per connection = %.2f, want ~3 (read pump + write pump + this test's reader)", perConn)
	}

	for _, c := range conns {
		_ = c.Close()
	}
	st.waitConns(5 * time.Second)
	settle()

	after := runtime.NumGoroutine()
	// The claim on the projector is that the gauge comes back down. Allow a
	// small slack for net/http's idle-connection bookkeeping.
	if after > baseline+4 {
		t.Errorf("goroutines did not return to baseline: %d -> %d -> %d", baseline, withClients, after)
	}
	t.Logf("census: baseline %d, with %d clients %d (%.2f/conn), after disconnect %d",
		baseline, n, withClients, perConn, after)
}

// ── GATE 5: one slow client must not cost the room anything ─────────────────

func TestSlowClientDoesNotStallTheRoom(t *testing.T) {
	base, st, done := boot(t, stackConfig{Seed: true, StatsInterval: 25 * time.Millisecond, KeyframeEvery: 10 * time.Millisecond})
	defer done()

	// One client that never reads, on a deliberately tiny receive buffer.
	slow := dialConstrained(t, base, "feed=fresh")
	defer slow.Close()

	// Nine healthy ones, plus a stats watcher.
	var mu sync.Mutex
	counts := make([]int, 9)
	healthy := make([]*websocket.Conn, 9)
	for i := 0; i < 9; i++ {
		c := dial(t, base, "feed=fresh")
		healthy[i] = c
		go drain(c, func([]byte) { mu.Lock(); counts[i]++; mu.Unlock() })
	}
	defer func() {
		for _, c := range healthy {
			_ = c.Close()
		}
	}()

	var latest wire.StatsInput
	watcher := dial(t, base, "feed=fresh")
	defer watcher.Close()
	go drain(watcher, func(b []byte) {
		var f struct {
			Type         string `json:"type"`
			Backpressure uint64 `json:"backpressure"`
		}
		if json.Unmarshal(b, &f) == nil && f.Type == "stats" {
			mu.Lock()
			latest.Backpressure = f.Backpressure
			mu.Unlock()
		}
	})

	trader := dial(t, base, "session=pump")
	go drain(trader, nil)
	defer trader.Close()
	for i := 0; i < 3000; i++ {
		_ = trader.WriteMessage(websocket.TextMessage,
			wire.SubmitCmd("buy", int64(10200+i%50), 50).Encode())
	}
	time.Sleep(2500 * time.Millisecond)

	mu.Lock()
	drops := latest.Backpressure
	got := append([]int(nil), counts...)
	mu.Unlock()

	for i, n := range got {
		if n == 0 {
			t.Errorf("healthy client %d received nothing — the slow client stalled the room", i)
		}
	}
	if drops == 0 {
		t.Error("the drop counter never moved; a client that never reads must cost ITSELF frames")
	}
	t.Logf("backpressure drops = %d, healthy clients received %v", drops, got)
	_ = st
}

// ── the chaos decorator, end to end ─────────────────────────────────────────

func TestChaosDelaysTheStaleLaneOnly(t *testing.T) {
	base, st, done := boot(t, stackConfig{Seed: true, StatsInterval: 25 * time.Millisecond, KeyframeEvery: 30 * time.Millisecond})
	defer done()

	type seen struct {
		mu   sync.Mutex
		last uint64
		n    int
	}
	var fresh, stale seen

	track := func(s *seen) func([]byte) {
		return func(b []byte) {
			var f struct {
				Type string `json:"type"`
				Seq  uint64 `json:"seq"`
			}
			if json.Unmarshal(b, &f) == nil && f.Type == "book" {
				s.mu.Lock()
				if f.Seq > s.last {
					s.last = f.Seq
				}
				s.n++
				s.mu.Unlock()
			}
		}
	}

	cf := dial(t, base, "feed=fresh")
	defer cf.Close()
	go drain(cf, track(&fresh))
	cs := dial(t, base, "feed=stale")
	defer cs.Close()
	go drain(cs, track(&stale))

	trader := dial(t, base, "session=chaos")
	go drain(trader, nil)
	defer trader.Close()

	// Undegraded: both lanes should track each other.
	for i := 0; i < 40; i++ {
		_ = trader.WriteMessage(websocket.TextMessage, wire.SubmitCmd("buy", int64(10200+i%30), 50).Encode())
		time.Sleep(3 * time.Millisecond)
	}
	time.Sleep(400 * time.Millisecond)
	fresh.mu.Lock()
	stale.mu.Lock()
	beforeGap := int64(fresh.last) - int64(stale.last)
	fresh.mu.Unlock()
	stale.mu.Unlock()

	// Arm chaos: 700ms behind, dropping one frame in three.
	st.Chaos.Configure(chaos.Policy{Armed: true, Delay: 700 * time.Millisecond, DropEvery: 3})
	for i := 0; i < 80; i++ {
		_ = trader.WriteMessage(websocket.TextMessage, wire.SubmitCmd("sell", int64(10300+i%30), 50).Encode())
		time.Sleep(3 * time.Millisecond)
	}
	time.Sleep(300 * time.Millisecond)

	fresh.mu.Lock()
	stale.mu.Lock()
	armedGap := int64(fresh.last) - int64(stale.last)
	fn, sn := fresh.n, stale.n
	fresh.mu.Unlock()
	stale.mu.Unlock()

	if armedGap <= beforeGap {
		t.Errorf("stale lane did not fall behind: gap %d before, %d armed", beforeGap, armedGap)
	}
	if sn >= fn {
		t.Errorf("stale lane received %d frames, fresh %d — chaos dropped nothing", sn, fn)
	}
	t.Logf("gap before %d, armed %d; frames fresh %d stale %d", beforeGap, armedGap, fn, sn)
}

// The engine must be untouched by any of it: the book the FRESH lane shows is
// still correct, and its invariant strip is still green. That is act two's
// entire claim, asserted rather than narrated.
func TestChaosNeverBreaksTheBook(t *testing.T) {
	base, st, done := boot(t, stackConfig{Seed: true, StatsInterval: 25 * time.Millisecond, KeyframeEvery: 20 * time.Millisecond})
	defer done()

	var mu sync.Mutex
	bad, books := 0, 0

	cf := dial(t, base, "feed=fresh")
	defer cf.Close()
	go drain(cf, func(b []byte) {
		var f struct {
			Type string `json:"type"`
			Inv  struct {
				Conservation bool `json:"conservation"`
				NoCross      bool `json:"no_cross"`
				Priority     bool `json:"priority"`
				Sweep        bool `json:"sweep"`
				Halted       bool `json:"halted"`
			} `json:"inv"`
		}
		if json.Unmarshal(b, &f) != nil || f.Type != "book" {
			return
		}
		mu.Lock()
		books++
		if !f.Inv.Conservation || !f.Inv.NoCross || !f.Inv.Priority || !f.Inv.Sweep || f.Inv.Halted {
			bad++
		}
		mu.Unlock()
	})

	st.Chaos.Configure(chaos.Policy{Armed: true, Delay: 400 * time.Millisecond, DropEvery: 2})

	trader := dial(t, base, "session=hammer")
	go drain(trader, nil)
	defer trader.Close()
	for i := 0; i < 500; i++ {
		side := "buy"
		if i%2 == 0 {
			side = "sell"
		}
		_ = trader.WriteMessage(websocket.TextMessage,
			wire.SubmitCmd(side, int64(10230+i%40), 50).Encode())
	}
	time.Sleep(900 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if books == 0 {
		t.Fatal("no book frames observed")
	}
	if bad != 0 {
		t.Fatalf("%d of %d book frames reported a failing invariant while chaos was armed", bad, books)
	}
	t.Logf("%d book frames under armed chaos, every invariant green", books)
}
