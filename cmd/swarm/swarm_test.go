package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/ziaulalam1/open-outcry/internal/wire"
	"go.uber.org/goleak"
)

func TestMain(m *testing.M) {
	// The load generator must not leak either. A rehearsal tool that quietly
	// accumulates goroutines becomes the bug you spend the afternoon chasing,
	// and it would do so while you are trying to diagnose the server.
	goleak.VerifyTestMain(m)
}

// testServer stands in for the real server, which does not exist until
// checkpoint 4. It upgrades, records every command it receives, replies to each
// submit with a `rested` frame so the canceller has something real to cancel,
// and pushes frames continuously so a reading client has something to read.
func testServer(t *testing.T) (addr string, received func() []wire.Command, stop func()) {
	t.Helper()

	up := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	var (
		mu   sync.Mutex
		got  []wire.Command
		next uint64
	)
	var conns sync.WaitGroup
	done := make(chan struct{})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		session := r.URL.Query().Get("session")
		c, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		conns.Add(1)
		defer conns.Done()
		defer c.Close()

		// One writer goroutine per connection, in the test server too — the rule
		// is not situational.
		sendDone := make(chan struct{})
		go func() {
			defer close(sendDone)
			tick := time.NewTicker(10 * time.Millisecond)
			defer tick.Stop()
			for {
				select {
				case <-done:
					return
				case <-tick.C:
					mu.Lock()
					next++
					id := next
					mu.Unlock()
					b, _ := json.Marshal(wire.ClientFrame{Type: "rested", ID: id, Session: session})
					_ = c.SetWriteDeadline(time.Now().Add(time.Second))
					if err := c.WriteMessage(websocket.TextMessage, b); err != nil {
						return
					}
				}
			}
		}()

		for {
			_, msg, err := c.ReadMessage()
			if err != nil {
				break
			}
			var cmd wire.Command
			if json.Unmarshal(msg, &cmd) == nil {
				mu.Lock()
				got = append(got, cmd)
				mu.Unlock()
			}
		}
		c.Close()
		<-sendDone
	}))

	return strings.TrimPrefix(srv.URL, "http://"),
		func() []wire.Command {
			mu.Lock()
			defer mu.Unlock()
			return append([]wire.Command(nil), got...)
		},
		func() { close(done); srv.Close(); conns.Wait() }
}

func baseCfg(addr string) Config {
	return Config{
		Addr: addr, Clients: 10, Blackholes: 2,
		Rate: 40, Duration: 900 * time.Millisecond,
		Seed: 7, Mid: 10250,
	}
}

func TestSwarmSendsWellFormedCommands(t *testing.T) {
	addr, received, stop := testServer(t)
	defer stop()

	stats := RunSwarm(context.Background(), baseCfg(addr))

	if len(stats) != 10 {
		t.Fatalf("got %d client stats, want 10", len(stats))
	}
	sent := 0
	for _, s := range stats {
		sent += s.Sent
		if s.Errors > 0 {
			t.Errorf("client %d (%s) had %d errors", s.Client, s.Profile, s.Errors)
		}
	}
	if sent == 0 {
		t.Fatal("no commands were sent")
	}

	cmds := received()
	if len(cmds) == 0 {
		t.Fatal("server received nothing")
	}
	submits, cancels := 0, 0
	for _, c := range cmds {
		switch c.Type {
		case "submit":
			submits++
			if c.Side != "buy" && c.Side != "sell" {
				t.Fatalf("bad side %q", c.Side)
			}
			if c.Price <= 0 {
				t.Fatalf("non-positive price %d — prices are int64 cents and must never be zero", c.Price)
			}
			if c.Qty <= 0 {
				t.Fatalf("non-positive qty %d", c.Qty)
			}
		case "cancel":
			cancels++
			if c.ID == 0 {
				t.Fatal("cancel with no order id")
			}
		default:
			t.Fatalf("unknown command type %q", c.Type)
		}
	}
	if submits == 0 {
		t.Fatal("no submits")
	}
	t.Logf("server received %d submits, %d cancels from %d clients", submits, cancels, len(stats))
}

// The whole reason the Blackhole profile exists. If these clients read anything,
// their receive buffers never fill, the server's send buffers never overflow,
// and the drop counter the projector displays has never been exercised.
func TestBlackholeClientsNeverRead(t *testing.T) {
	addr, _, stop := testServer(t)
	defer stop()

	stats := RunSwarm(context.Background(), baseCfg(addr))

	blackholes, readers := 0, 0
	for _, s := range stats {
		if s.Profile == Blackhole {
			blackholes++
			if s.Read != 0 {
				t.Errorf("blackholed client %d read %d frames, want 0", s.Client, s.Read)
			}
			if s.Sent == 0 {
				t.Errorf("blackholed client %d sent nothing; it must still submit orders", s.Client)
			}
			continue
		}
		if s.Read > 0 {
			readers++
		}
	}
	if blackholes != 2 {
		t.Fatalf("got %d blackholed clients, want 2", blackholes)
	}
	// The control: normal clients DO read, so "read 0 frames" is a property of
	// the profile and not of a server that never sent anything.
	if readers == 0 {
		t.Fatal("no non-blackhole client read anything — the test server is not pushing frames")
	}
}

func TestProfileMixIsBalanced(t *testing.T) {
	cfg := Config{Clients: 20, Blackholes: 1}
	counts := map[Profile]int{}
	for _, p := range assignProfiles(cfg) {
		counts[p]++
	}
	for _, p := range []Profile{Rester, Crosser, Canceller, Blackhole} {
		if counts[p] == 0 {
			t.Errorf("no %s clients; a room of one behaviour proves nothing", p)
		}
	}
	if counts[Blackhole] != 1 {
		t.Errorf("blackholes = %d, want 1", counts[Blackhole])
	}
	if counts[Crosser] == 0 {
		t.Error("without crossers there are no trades, and nothing to check invariants against")
	}
}

// Asking for a blackhole must produce one even at small client counts, because
// the small-room rehearsal is exactly when you would otherwise not notice.
func TestBlackholeIsGuaranteedEvenWithOneClient(t *testing.T) {
	got := assignProfiles(Config{Clients: 1, Blackholes: 1})
	if len(got) != 1 || got[0] != Blackhole {
		t.Fatalf("got %v, want [blackhole]", got)
	}
}

func TestSwarmStopsOnContextCancel(t *testing.T) {
	addr, _, stop := testServer(t)
	defer stop()

	cfg := baseCfg(addr)
	cfg.Duration = time.Hour // would never end on its own

	ctx, cancel := context.WithCancel(context.Background())
	start := time.Now()
	go func() { time.Sleep(250 * time.Millisecond); cancel() }()

	stats := RunSwarm(ctx, cfg)
	elapsed := time.Since(start)

	if elapsed > 5*time.Second {
		t.Fatalf("swarm took %s to stop after cancel", elapsed)
	}
	if len(stats) != cfg.Clients {
		t.Fatalf("got %d stats, want %d — every client must report even when cancelled",
			len(stats), cfg.Clients)
	}
}

func TestSwarmSurvivesAnUnreachableServer(t *testing.T) {
	cfg := baseCfg("127.0.0.1:1") // nothing is listening
	cfg.Duration = 300 * time.Millisecond

	stats := RunSwarm(context.Background(), cfg)

	if len(stats) != cfg.Clients {
		t.Fatalf("got %d stats, want %d", len(stats), cfg.Clients)
	}
	for _, s := range stats {
		if s.Errors == 0 {
			t.Errorf("client %d reported no error against a dead server", s.Client)
		}
	}
}
