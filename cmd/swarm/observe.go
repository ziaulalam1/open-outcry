package main

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// ServerCounters is what the SERVER says about delivery, read off its own stats
// feed rather than inferred.
//
// This type exists because of a defect in the previous version of this program:
// it printed
//
//	"the server's send buffers for those connections should have overflowed
//	 and been counted"
//
// and had no way whatsoever to check whether they had. That sentence is a claim
// about a number the tool could not see, phrased so it reads like a result. It
// is the same failure this project has now found three times in its own
// instruments (docs/build-log.md entries 5, 10 and 13): an instrument that is
// wrong, an instrument that is dead, an instrument more generous than
// production. This one was simply making it up.
//
// The fix is not better wording. It is to go and read the counter.
type ServerCounters struct {
	Observed bool // false means the observer never got a stats frame

	FirstBackpressure uint64
	LastBackpressure  uint64
	FirstChaosDropped uint64
	LastChaosDropped  uint64

	Clients      int
	EngineSeq    uint64
	ChaosDelayMS int
	Split        bool
	Samples      int
}

// Backpressure is the delta over the run: the projector's "dropped · slow phone".
func (s ServerCounters) Backpressure() uint64 {
	return s.LastBackpressure - s.FirstBackpressure
}

// ChaosDropped is the delta over the run: the projector's "dropped · chaos".
func (s ServerCounters) ChaosDropped() uint64 {
	return s.LastChaosDropped - s.FirstChaosDropped
}

// statsFrame is the subset of the server's stats frame this tool reads.
//
// Deliberately a subset. Naming only the fields that are actually used means a
// server-side addition cannot silently change what this reports.
type statsFrame struct {
	Type         string `json:"type"`
	Clients      int    `json:"clients"`
	Backpressure uint64 `json:"backpressure"`
	ChaosDropped uint64 `json:"chaos_dropped"`
	ChaosDelayMS int    `json:"chaos_delay_ms"`
	EngineSeq    uint64 `json:"engine_seq"`
	Split        bool   `json:"split"`
}

// Observer holds one extra connection for the life of the run, subscribed to
// the stats topic, so the summary can report measured numbers.
//
// It is a reader, not a trader: it sends nothing and is not counted among the
// simulated attendees. It does add one connection to the server's client count,
// which is why the summary prints the server's own client figure rather than
// quietly assuming it equals -n.
type Observer struct {
	mu   sync.Mutex
	c    ServerCounters
	conn *websocket.Conn
	// done is closed once the reader goroutine has finished its closing
	// handshake. Stop() waits on it.
	//
	// Without this the process can return from main while the handshake is
	// still in flight, the OS tears the socket down, and the server logs 1006
	// for the observer — which is the exact defect this file's sibling change
	// exists to remove, reintroduced by the fix for it. Caught by the server
	// log on the first confirmation run.
	done chan struct{}
}

// StartObserver dials the stats feed. A failure here is not fatal: the swarm's
// job is to generate load, and it must still do that if the observer cannot
// connect. The summary then says the counters were NOT observed rather than
// printing a zero that would read as "nothing was dropped".
func StartObserver(ctx context.Context, addr string) *Observer {
	o := &Observer{done: make(chan struct{})}

	url := "ws://" + addr + "/ws?feed=fresh&session=swarm-observer"
	dialer := websocket.Dialer{HandshakeTimeout: 5 * time.Second}
	conn, _, err := dialer.DialContext(ctx, url, http.Header{})
	if err != nil {
		close(o.done)
		return o // Observed stays false
	}
	o.conn = conn

	go func() {
		defer close(o.done)
		defer shutdownConn(conn, nil, false)
		for {
			if ctx.Err() != nil {
				return
			}
			_, msg, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var f statsFrame
			if json.Unmarshal(msg, &f) != nil || f.Type != "stats" {
				continue
			}
			o.mu.Lock()
			if !o.c.Observed {
				o.c.Observed = true
				o.c.FirstBackpressure = f.Backpressure
				o.c.FirstChaosDropped = f.ChaosDropped
			}
			o.c.LastBackpressure = f.Backpressure
			o.c.LastChaosDropped = f.ChaosDropped
			o.c.Clients = f.Clients
			o.c.EngineSeq = f.EngineSeq
			o.c.ChaosDelayMS = f.ChaosDelayMS
			o.c.Split = f.Split
			o.c.Samples++
			o.mu.Unlock()
		}
	}()

	return o
}

// Counters returns a snapshot. Safe to call while the observer is still running.
func (o *Observer) Counters() ServerCounters {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.c
}

// Stop ends the observer.
//
// It unblocks the read rather than closing the socket, so that the goroutine's
// own deferred shutdownConn still performs the closing handshake. Closing here
// would drop TCP under the reader and produce exactly the 1006 on the server
// that this change exists to eliminate.
func (o *Observer) Stop() {
	o.mu.Lock()
	conn := o.conn
	o.mu.Unlock()
	if conn != nil {
		_ = conn.SetReadDeadline(time.Now().Add(time.Millisecond))
	}
	// Wait for the handshake to finish. Bounded at twice closeGrace: one grace
	// period for our close frame, one for the peer's reply.
	select {
	case <-o.done:
	case <-time.After(2 * closeGrace):
	}
}
