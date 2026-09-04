// Command open-outcry runs the workshop: a matching engine, a fan-out hub, a
// chaos decorator, and two views served from a single binary.
//
// This is the composition root — the one file that sees every package at once,
// and the only place where the wiring exists. Everything it assembles was built
// to be assembled here and nowhere else: the engine does not know a hub exists,
// the hub cannot name a domain type, and the chaos line's only outward
// reference is a function value.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"runtime/pprof"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
	openoutcry "github.com/ziaulalam1/open-outcry"
	"github.com/ziaulalam1/open-outcry/internal/engine"
	"github.com/ziaulalam1/open-outcry/internal/loop"
	"github.com/ziaulalam1/open-outcry/internal/wire"
)

const (
	title    = "OPEN OUTCRY"
	subtitle = "Long Island University · Live Order Book Workshop"

	// Derived from hub.writeWait (2s): worst-case per-connection teardown is two
	// write deadlines, so five seconds of drain is 2x the worst case plus slack.
	// Guessing these numbers is how a shutdown either hangs or truncates.
	drainBudget = 5 * time.Second
	totalBudget = 6 * time.Second
)

func main() {
	port := flag.Int("port", 8080, "listen port")
	flag.Parse() // the only configuration in this repository

	rootCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	st := newStack(stackConfig{Seed: true})
	st.start(rootCtx)

	srv := &http.Server{
		Addr:              fmt.Sprintf(":%d", *port),
		Handler:           routes(st, *port),
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal(err)
		}
	}()

	lan := lanURL(*port)
	log.Printf("open-outcry listening on :%d", *port)
	log.Printf("  projector  %s/?view=projector", lan)
	log.Printf("  trader     %s/?view=trader", lan)
	log.Printf("  chaos      %s/chaos?armed=1&delay=1200&drop=3", lan)

	<-rootCtx.Done()
	stop() // a second ^C now kills the process instead of being swallowed
	log.Print("draining; ^C again to force")

	shutdown(srv, st)
}

// shutdown runs in data-flow order with one deliberate inversion.
//
// The fan-out layer is torn down BEFORE the engine, because it owns the sockets,
// and closing sockets is the only way to unblock the read pumps that feed the
// engine. Only after every connection is gone does eng.cmds provably have zero
// senders, which is what makes its final drain finite.
func shutdown(srv *http.Server, st *stack) {
	ctx, cancel := context.WithTimeout(context.Background(), totalBudget)
	defer cancel()

	// (1) Stop accepting, and finish plain HTTP requests. Per the stdlib docs
	// this does NOT close or wait for hijacked connections such as WebSockets —
	// a shutdown that stops here returns instantly and leaks every socket.
	_ = srv.Shutdown(ctx)

	// (2) The hub, cancelled by rootCtx, has closed every client's send channel.
	// (3) Write pumps sent close frames and closed their sockets.
	// (4) Read pumps unblocked and returned.
	if !st.waitConns(drainBudget) {
		// Never fail silently on a leak: dump the parked stacks so the reason is
		// in the log rather than in someone's memory of the evening.
		_ = pprof.Lookup("goroutine").WriteTo(os.Stderr, 2)
		log.Print("connections did not drain within budget")
	}

	// (5) Zero senders remain on the command channel, so this drain terminates.
	st.stop()
	log.Print("clean shutdown")
}

// ── routes ──────────────────────────────────────────────────────────────────

var upgrader = websocket.Upgrader{
	HandshakeTimeout: 3 * time.Second,
	ReadBufferSize:   1024,
	WriteBufferSize:  1024,
	// Deliberate, not an oversight: phones reach the laptop over the venue LAN,
	// there are no cookies and no auth, so cross-site hijacking has nothing to
	// hijack. Documented here so nobody "fixes" it into a same-origin check that
	// blocks every phone in the room.
	CheckOrigin: func(*http.Request) bool { return true },
}

func routes(st *stack, port int) http.Handler {
	mux := http.NewServeMux()

	sub, err := fs.Sub(openoutcry.Web, "web")
	if err != nil {
		log.Fatal(err)
	}
	mux.Handle("/", http.FileServer(http.FS(sub)))

	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	// The presenter's control surface. Reachable from a phone, because the
	// presenter is standing up and holding one.
	mux.HandleFunc("/chaos", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		p := st.Chaos.Snapshot()
		if v := q.Get("armed"); v != "" {
			p.Armed = v == "1" || v == "true"
		}
		if v, err := strconv.Atoi(q.Get("delay")); err == nil {
			p.Delay = time.Duration(v) * time.Millisecond
		}
		if v, err := strconv.Atoi(q.Get("drop")); err == nil {
			p.DropEvery = v
		}
		st.Chaos.Configure(p)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"armed":%v,"delay_ms":%d,"drop_every":%d}`,
			p.Armed, p.Delay/time.Millisecond, p.DropEvery)
	})

	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		feed := r.URL.Query().Get("feed")
		session := r.URL.Query().Get("session")
		if session == "" {
			session = randomSession()
		}

		var topics []string
		switch feed {
		case "fresh":
			topics = []string{loop.TopicBookFresh, loop.TopicStats, loop.TopicHalt}
		case "stale":
			topics = []string{loop.TopicBookStale, loop.TopicHalt}
		default: // a phone
			topics = []string{loop.TopicTop, loop.TopicSession(session)}
		}

		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return // Upgrade has already written a response
		}

		st.conns.Add(1)
		defer st.conns.Done()

		hello := st.Enc.Hello(session, lanURL(port), title, subtitle, nil)

		// Serve runs the read pump on THIS goroutine — the handler goroutine
		// becomes the connection's supervisor, which gives the WaitGroup an
		// obviously correct home and keeps the census at two goroutines per
		// connection instead of three.
		st.Hub.Serve(conn, session, topics, hello, func(msg []byte) {
			cmd, err := wire.DecodeCommand(engine.SessionID(session), msg)
			if err != nil {
				return // malformed input from a phone is not an event
			}
			if err := st.Loop.Submit(cmd); err != nil {
				// Inbound policy is reject, never silent drop: a trader seeing
				// "submitted" for an order that never reached the book is a
				// correctness bug wearing a delivery bug's clothes.
				st.Hub.Publish(loop.TopicSession(session), st.Enc.Busy(session))
			}
		})
	})

	return mux
}

// ── helpers ─────────────────────────────────────────────────────────────────

// lanURL finds the address a phone can actually reach.
//
// The server is the only party that knows this: the browser rendering the QR
// code has no idea which interface the laptop is on, and "localhost" is useless
// to everyone in the room.
func lanURL(port int) string {
	addrs, err := net.InterfaceAddrs()
	if err == nil {
		for _, a := range addrs {
			n, ok := a.(*net.IPNet)
			if !ok || n.IP.IsLoopback() || n.IP.To4() == nil {
				continue
			}
			return fmt.Sprintf("http://%s:%d", n.IP.String(), port)
		}
	}
	return fmt.Sprintf("http://localhost:%d", port)
}

const sessionAlphabet = "abcdefghjkmnpqrstuvwxyz23456789"

var sessionCounter struct {
	mu sync.Mutex
	n  uint64
}

// randomSession is deliberately boring: a session id is a label, not a secret.
// No login, no names — a phone that reconnects keeps its own id from
// sessionStorage, and a phone that does not gets a fresh one.
func randomSession() string {
	sessionCounter.mu.Lock()
	sessionCounter.n++
	n := sessionCounter.n
	sessionCounter.mu.Unlock()

	out := make([]byte, 3)
	for i := range out {
		out[i] = sessionAlphabet[n%uint64(len(sessionAlphabet))]
		n /= uint64(len(sessionAlphabet))
	}
	return string(out)
}
