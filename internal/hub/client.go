package hub

import (
	"log"
	"time"

	"github.com/gorilla/websocket"
)

// Timeouts. Every duration in the shutdown path is derived from writeWait
// rather than guessed: worst case per-connection teardown is 2 x writeWait,
// which is where the drain budget in main comes from.
const (
	writeWait  = 2 * time.Second
	pongWait   = 25 * time.Second
	pingPeriod = 20 * time.Second // must be < pongWait
	maxMsgSize = 4096
)

type Client struct {
	id     string
	topics []string
	conn   *websocket.Conn
	send   chan []byte

	// dropped is a plain uint64, not an atomic. It is written and read by the
	// hub goroutine alone — see Hub.trySend.
	dropped uint64
}

func (c *Client) ID() string { return c.id }

func (c *Client) subscribed(topic string) bool {
	for _, t := range c.topics {
		if t == topic {
			return true
		}
	}
	return false
}

// Serve runs one connection to completion.
//
// The HTTP handler goroutine BECOMES the read pump: after Upgrade, net/http
// never touches it again, so spawning a third goroutine would buy nothing and
// cost the natural join point. Two goroutines per connection, not three, which
// is what makes the steady-state census 6 + 2N rather than 6 + 3N.
func (h *Hub) Serve(conn *websocket.Conn, id string, topics []string, hello []byte, onCommand func([]byte)) {
	c := &Client{id: id, topics: topics, conn: conn, send: make(chan []byte, SendBuffer)}

	writerDone := make(chan struct{})
	go func() { defer close(writerDone); c.writePump() }()

	// hello is queued BEFORE registration, while this goroutine is still the
	// only sender on c.send. After registration only the hub sends. Doing it the
	// other way round lets a broadcast land ahead of hello — and hello carries
	// px_scale, so a pre-hello frame renders prices wrong.
	if hello != nil {
		c.send <- hello
	}

	select {
	case h.reg <- c:
	case <-h.done:
		// The hub is gone, so it will never close c.send. This goroutine is
		// still the only sender, so closing here is safe and is the only way to
		// stop the write pump.
		close(c.send)
		<-writerDone
		return
	}

	c.readPump(h, onCommand)
	<-writerDone // no goroutine outlives its connection
}

// readPump owns the READ side. It never touches c.send and never closes the
// connection: detecting a disconnect and tearing one down are different jobs,
// and fusing them is precisely the bug in every chat-server tutorial.
func (c *Client) readPump(h *Hub, onCommand func([]byte)) {
	c.conn.SetReadLimit(maxMsgSize)
	_ = c.conn.SetReadDeadline(time.Now().Add(pongWait))
	// The pong handler runs INSIDE ReadMessage, on this goroutine, which is
	// exactly why touching the read deadline from here is not a race.
	c.conn.SetPongHandler(func(string) error {
		return c.conn.SetReadDeadline(time.Now().Add(pongWait))
	})
	// Deliberately NOT overriding SetCloseHandler. Gorilla's default echoes the
	// peer's close frame using WriteControl, the one write method documented as
	// safe alongside the write pump. Replacing it with WriteMessage would
	// reintroduce the concurrent-write hazard through a side door, and it would
	// fire only when a client disconnects mid-broadcast — the hardest possible
	// version of that bug to reproduce.

	defer func() {
		select {
		case h.unreg <- c: // cannot block indefinitely: the hub never blocks
		case <-h.done: // hub already gone; nothing to deregister from
		}
	}()

	for {
		_, msg, err := c.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
				log.Printf("ws read %s: %v", c.id, err)
			}
			return
		}
		if onCommand != nil {
			onCommand(msg)
		}
	}
}

// writePump owns the WRITE side, and is the ONLY goroutine that ever calls
// WriteMessage on this connection.
//
// gorilla detects concurrent writes with a non-atomic best-effort flag
// (conn.go: `isWriting bool // for best-effort concurrent write detection`),
// which is why the resulting panic is intermittent rather than deterministic —
// and why the panic is an unreliable canary: the same race can silently
// interleave frames and corrupt the stream without ever tripping it.
//
// It takes no context. One goroutine, one exit reason; a second exit path is
// how double-close bugs are born. Shutdown reaches it by closing c.send.
func (c *Client) writePump() {
	ping := time.NewTicker(pingPeriod)
	defer func() {
		ping.Stop()
		_ = c.conn.Close() // unblocks the read pump; safe per gorilla's docs
	}()

	for {
		select {
		case b, ok := <-c.send:
			if !ok {
				// Exactly one meaning: the hub has retired this client. Do NOT
				// drain — a slow client may hold dozens of stale frames and
				// shutdown must stay bounded. Do NOT wait for the peer's close
				// echo either: RFC 6455 lets a server close whenever it chooses,
				// and a phone on dead wifi never echoes.
				_ = c.conn.WriteControl(websocket.CloseMessage,
					websocket.FormatCloseMessage(websocket.CloseGoingAway, "server shutting down"),
					time.Now().Add(writeWait))
				return
			}
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.TextMessage, b); err != nil {
				return // the defer closes the conn; the read pump follows
			}

		case <-ping.C:
			// WriteControl carries its own deadline and cannot interleave with a
			// partially written data frame.
			if err := c.conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(writeWait)); err != nil {
				return
			}
		}
	}
}
