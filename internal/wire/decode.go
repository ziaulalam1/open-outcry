package wire

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/ziaulalam1/open-outcry/internal/engine"
)

// Limits applied at the edge, before anything reaches the engine.
//
// The engine rejects bad values on its own, so these are not the correctness
// boundary — they are the blast radius. A phone that sends a nine-quintillion
// share order should be turned away here rather than being accepted, ledgered,
// and then overflowing an int64 somewhere downstream. Rejecting at the edge also
// means the malformed case never consumes a sequence number, which keeps the
// replay log clean.
const (
	MaxQty   = 1_000_000
	MaxPrice = 100_000_000 // $1,000,000.00 in cents
)

var (
	ErrBadJSON    = errors.New("malformed json")
	ErrBadType    = errors.New("unknown command type")
	ErrBadSide    = errors.New("side must be buy or sell")
	ErrOutOfRange = errors.New("price or quantity out of range")
)

// Command is the inbound wire shape, and it is EXPORTED on purpose.
//
// cmd/swarm encodes exactly the struct the server decodes. Until checkpoint 4
// swarm carried its own copy of these five fields, which is the classic quiet
// second source of truth: both sides work, then one side gains a field, and the
// mismatch surfaces as an order that silently does nothing. One definition, and
// a round-trip test pinning encode against decode, removes the possibility
// rather than the symptom.
type Command struct {
	Type  string `json:"type"`
	Side  string `json:"side,omitempty"`
	Price int64  `json:"price,omitempty"`
	Qty   int64  `json:"qty,omitempty"`
	ID    uint64 `json:"id,omitempty"`
}

// Encode is the client side of the protocol.
func (c Command) Encode() []byte {
	b, _ := json.Marshal(c)
	return b
}

// SubmitCmd and CancelCmd are the two things a phone can say.
func SubmitCmd(side string, price, qty int64) Command {
	return Command{Type: "submit", Side: side, Price: price, Qty: qty}
}

func CancelCmd(id uint64) Command { return Command{Type: "cancel", ID: id} }

// ClientFrame is the subset of an OUTBOUND frame a client needs in order to
// route it. Exported for the same reason Command is.
type ClientFrame struct {
	Type    string `json:"type"`
	ID      uint64 `json:"id"`
	Session string `json:"session"`
	Side    string `json:"side"`
	Px      int64  `json:"px"`
	Qty     int64  `json:"qty"`
}

// DecodeClientFrame is lenient by design: a client that cannot parse one frame
// must keep reading the socket, not tear the connection down.
func DecodeClientFrame(b []byte) (ClientFrame, bool) {
	var f ClientFrame
	if err := json.Unmarshal(b, &f); err != nil {
		return ClientFrame{}, false
	}
	return f, f.Type != ""
}

// DecodeCommand turns bytes from a phone into a domain value, or fails.
//
// This function is the entire reason the engine has never heard of JSON. Every
// transport concern — encoding, validation, range limits, hostile input — is
// resolved here, and what crosses the boundary is an engine.Command that is
// already known to be well-formed.
func DecodeCommand(session engine.SessionID, b []byte) (engine.Command, error) {
	var in Command
	dec := json.NewDecoder(newLimitedReader(b))
	// A field the client sends that we do not understand is a protocol mismatch,
	// not something to silently ignore: it usually means a stale phone is still
	// running last week's build.
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrBadJSON, err)
	}

	switch in.Type {
	case "submit":
		var side engine.Side
		switch in.Side {
		case "buy":
			side = engine.Buy
		case "sell":
			side = engine.Sell
		default:
			return nil, ErrBadSide
		}
		if in.Qty <= 0 || in.Qty > MaxQty || in.Price <= 0 || in.Price > MaxPrice {
			return nil, ErrOutOfRange
		}
		return engine.Submit{
			Session: session,
			Side:    side,
			Price:   engine.Ticks(in.Price),
			Qty:     engine.Qty(in.Qty),
		}, nil

	case "cancel":
		if in.ID == 0 {
			return nil, ErrOutOfRange
		}
		// The session comes from the CONNECTION, never from the message body.
		// Taking it from the payload would let any phone cancel anyone's order
		// by guessing an id.
		return engine.Cancel{Session: session, ID: engine.OrderID(in.ID)}, nil
	}
	return nil, ErrBadType
}
