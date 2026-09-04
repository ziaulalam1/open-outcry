package main

import (
	"context"
	"fmt"
	"math/rand"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/ziaulalam1/open-outcry/internal/wire"
)

// Profile is how one simulated attendee behaves.
//
// The mix matters more than the count. A room of thirty people all resting
// passive orders never crosses the spread and never produces a trade, so the
// projector shows a deep, still book and proves nothing. These four profiles
// between them produce depth, trades, churn, and one genuinely broken consumer.
type Profile int

const (
	// Rester posts inside the book and waits. Provides the depth that makes the
	// ladder photograph well.
	Rester Profile = iota
	// Crosser lifts offers and hits bids. Provides the trades.
	Crosser
	// Canceller posts and pulls, repeatedly. Provides churn, and exercises the
	// one term in the conservation identity that leaves the book without trading.
	Canceller
	// Blackhole connects, submits, and then NEVER READS ITS SOCKET.
	//
	// This is the only profile that matters for requirement 3. A slow phone is
	// hard to simulate honestly and impossible to summon on demand; a client
	// that never drains its receive buffer is trivial to create and is strictly
	// worse than any real phone. Without at least one of these, the drop counter
	// on the projector is a number that has never once been non-zero, and the
	// whole of act two rests on it.
	Blackhole
)

func (p Profile) String() string {
	switch p {
	case Rester:
		return "rester"
	case Crosser:
		return "crosser"
	case Canceller:
		return "canceller"
	case Blackhole:
		return "blackhole"
	}
	return "unknown"
}

type Config struct {
	Addr       string
	Clients    int
	Blackholes int
	Rate       float64 // orders per second, per client
	Duration   time.Duration
	Seed       int64
	Mid        int64 // reference price in cents

}

type Stats struct {
	Client  int
	Profile Profile
	Session string
	Sent    int
	Read    int
	Errors  int
	// Closed records that the SERVER hung up on this client, which is a
	// different fact from an error and is deliberately kept in its own column.
	//
	// For a blackhole it is the design working: it never reads, so it never
	// pongs, so the server's read deadline (pongWait) expires and the server
	// disconnects it. Counting that as an error made every correct run report
	// a failure, which is the fastest way to teach someone to ignore the errors
	// column entirely.
	Closed int
}

// closeGrace bounds the closing handshake. Two seconds matches the server's own
// writeWait, so neither side waits longer than the other is willing to spend.
const closeGrace = 2 * time.Second

// shutdownConn performs the WebSocket CLOSING HANDSHAKE rather than dropping TCP.
//
// gorilla does not do this for you, and the difference is not cosmetic. Sending
// a close frame and then immediately calling Close() is a race: Close() tears
// down the TCP connection, and when it wins, the peer's read pump reaches EOF
// having never seen the close frame and reports 1006 (abnormal closure) for a
// shutdown that was in fact completely orderly.
//
// The race is decided by load and by kernel buffering, which is why it can be
// invisible on one machine and fire on all N connections at once on another —
// the worst possible failure mode, because "it works here" is true and useless.
//
// Correct order: send our close frame, wait for the peer's, then drop TCP.
func shutdownConn(conn *websocket.Conn, readDone <-chan struct{}, hasReader bool) {
	err := conn.WriteControl(websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
		time.Now().Add(closeGrace))
	if err != nil {
		_ = conn.Close() // peer already gone; nothing to hand shake with
		return
	}

	if hasReader {
		// The reader goroutine is still running. The peer's close frame makes
		// its ReadMessage return, which closes readDone. Bounded, because a
		// peer that never answers must not hang the shutdown.
		select {
		case <-readDone:
		case <-time.After(closeGrace):
		}
	} else {
		// The blackhole has no reader by construction. Drain inline until the
		// peer's close frame arrives or the grace period expires.
		//
		// This reads AFTER the measured run has ended, so it does not weaken
		// the "never reads its socket" property that the whole profile exists
		// for, and the frames are discarded rather than counted.
		_ = conn.SetReadDeadline(time.Now().Add(closeGrace))
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				break
			}
		}
	}
	_ = conn.Close()
}

// isPeerClose reports whether an error means the peer hung up, as opposed to
// something going wrong. Both end the client's loop; only one is a failure.
func isPeerClose(err error) bool {
	if err == nil {
		return false
	}
	if websocket.IsCloseError(err,
		websocket.CloseNormalClosure,
		websocket.CloseGoingAway,
		websocket.CloseAbnormalClosure,
		websocket.CloseNoStatusReceived) {
		return true
	}
	s := err.Error()
	for _, frag := range []string{
		"broken pipe",
		"connection reset by peer",
		"use of closed network connection",
		"unexpected EOF",
		"EOF",
	} {
		if strings.Contains(s, frag) {
			return true
		}
	}
	return false
}

// The wire shapes that used to live here are gone.
//
// swarm now encodes wire.Command, the exact struct the server decodes, and
// reads wire.ClientFrame. That was carried as deliberate debt through
// checkpoint 3 with a named payoff point, because internal/wire did not exist
// yet and pulling the whole transport layer forward to avoid one duplicated
// struct would have been the worse trade. Retiring it is the payoff: one
// definition, pinned by a round-trip test in internal/wire.

func assignProfiles(cfg Config) []Profile {
	out := make([]Profile, cfg.Clients)
	for i := range out {
		switch i % 5 {
		case 0, 1:
			out[i] = Rester // depth is the most common behaviour in a real room
		case 2, 3:
			out[i] = Crosser
		default:
			out[i] = Canceller
		}
	}
	// Blackholes are forced in at the END, overwriting whatever was there, so
	// that asking for one always gets one even at small client counts.
	for i := 0; i < cfg.Blackholes && i < len(out); i++ {
		out[len(out)-1-i] = Blackhole
	}
	return out
}

// RunSwarm dials cfg.Clients connections and drives them until ctx is done.
//
// Every goroutine started here has an owner and an exit path, for the same
// reason the server does: this is the thing that will be running when the demo
// is being rehearsed, and a load generator that leaks is a load generator that
// eventually becomes the bug you are chasing.
func RunSwarm(ctx context.Context, cfg Config) []Stats {
	profiles := assignProfiles(cfg)
	stats := make([]Stats, cfg.Clients)

	var wg sync.WaitGroup
	for i := 0; i < cfg.Clients; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			// Each client gets its own rand source. A shared one would be a data
			// race, and math/rand's global is a lock — either way it would make
			// the load generator's own contention part of the measurement.
			rng := rand.New(rand.NewSource(cfg.Seed + int64(i)*7919))
			stats[i] = runClient(ctx, cfg, i, profiles[i], rng)
		}(i)
	}
	wg.Wait()
	return stats
}

func runClient(ctx context.Context, cfg Config, idx int, p Profile, rng *rand.Rand) Stats {
	st := Stats{Client: idx, Profile: p, Session: fmt.Sprintf("swarm-%02d", idx)}

	// A blackholed client subscribes to the BUSIEST feed, and that choice is the
	// only thing that makes the drop policy observable at all. Measured, on one
	// fresh server per condition, 12 clients at 30 orders/sec for 45s:
	//
	//     blackhole on the projector feed .... 1337 drops
	//     blackhole on the trader feed ....... 0 drops
	//
	// The trader subscription is the lowest-volume one in the system — a few
	// small top-of-book frames — and the server's own send buffer absorbs all of
	// it, so nothing ever backs up. The projector feed carries full book
	// snapshots, which is enough volume to fill a socket that nobody is draining.
	//
	// A separate attempt to force this with a small SO_RCVBUF on the client was
	// removed after measurement: at the same feed and rate it made no difference
	// (1352 without, 1312 with — noise). Throughput is the variable; the socket
	// buffer is not. See docs/build-log.md entry 14.
	feed := "view=trader"
	if p == Blackhole {
		feed = "feed=fresh"
	}
	url := fmt.Sprintf("ws://%s/ws?%s&session=%s", cfg.Addr, feed, st.Session)
	dialer := websocket.Dialer{HandshakeTimeout: 5 * time.Second}
	conn, _, err := dialer.DialContext(ctx, url, http.Header{})
	if err != nil {
		st.Errors++
		return st
	}
	defer conn.Close()

	// Own orders, so the canceller cancels something that actually exists.
	var (
		mu   sync.Mutex
		mine []uint64
		read int
	)

	readDone := make(chan struct{})
	if p == Blackhole {
		// No reader. The receive buffer fills, the server's writes to this
		// client start failing or blocking, and its send buffer overflows —
		// which is precisely the condition the drop counter exists to report.
		close(readDone)
	} else {
		go func() {
			defer close(readDone)
			for {
				_, msg, err := conn.ReadMessage()
				if err != nil {
					return
				}
				mu.Lock()
				read++
				if in, ok := wire.DecodeClientFrame(msg); ok &&
					in.Type == "rested" && in.Session == st.Session {
					mine = append(mine, in.ID)
				}
				mu.Unlock()
			}
		}()
	}

	interval := time.Duration(float64(time.Second) / cfg.Rate)
	if interval <= 0 {
		interval = 100 * time.Millisecond
	}
	tick := time.NewTicker(interval)
	defer tick.Stop()

	deadline := time.NewTimer(cfg.Duration)
	defer deadline.Stop()

	// This goroutine is the connection's ONLY writer. Same rule as the server,
	// for the same reason: concurrent writes to a gorilla connection corrupt the
	// frame stream, and the panic that is supposed to catch it is a best-effort
	// non-atomic flag rather than a lock.
loop:
	for {
		select {
		case <-ctx.Done():
			break loop
		case <-deadline.C:
			break loop
		case <-tick.C:
			cmd, ok := nextCommand(p, cfg, rng, &mu, &mine)
			if !ok {
				continue
			}
			_ = conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
			if err := conn.WriteMessage(websocket.TextMessage, cmd.Encode()); err != nil {
				// A write failing because the peer hung up is not this tool
				// malfunctioning, and the summary must not say it is. The
				// blackhole reaches here on every run by design.
				if isPeerClose(err) || ctx.Err() != nil {
					st.Closed++
				} else {
					st.Errors++
				}
				break loop
			}
			st.Sent++
		}
	}

	shutdownConn(conn, readDone, p != Blackhole)
	<-readDone // no goroutine outlives its client

	mu.Lock()
	st.Read = read
	mu.Unlock()
	return st
}

func nextCommand(p Profile, cfg Config, rng *rand.Rand, mu *sync.Mutex, mine *[]uint64) (wire.Command, bool) {
	side := "buy"
	if rng.Intn(2) == 1 {
		side = "sell"
	}
	qty := int64((1 + rng.Intn(6)) * 50)
	tick := int64(5)

	switch p {
	case Canceller:
		mu.Lock()
		n := len(*mine)
		var id uint64
		if n > 0 {
			k := rng.Intn(n)
			id = (*mine)[k]
			*mine = append((*mine)[:k], (*mine)[k+1:]...)
		}
		mu.Unlock()
		if id != 0 {
			return wire.CancelCmd(id), true
		}
		// Nothing to cancel yet: post something so there will be next time.
		fallthrough

	case Rester, Blackhole:
		// Rest away from the touch so it provides depth rather than trades.
		off := int64(1+rng.Intn(5)) * tick
		px := cfg.Mid - off
		if side == "sell" {
			px = cfg.Mid + off
		}
		return wire.SubmitCmd(side, px, qty), true

	case Crosser:
		// Reach across the spread. This is what makes trades happen, which is
		// what makes the tape, the trade counter and the invariant checks mean
		// anything at all.
		off := int64(1+rng.Intn(3)) * tick
		px := cfg.Mid + off
		if side == "sell" {
			px = cfg.Mid - off
		}
		return wire.SubmitCmd(side, px, qty), true
	}
	return wire.Command{}, false
}
