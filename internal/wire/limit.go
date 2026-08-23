package wire

import (
	"bytes"
	"io"
)

// MaxCommandBytes bounds a single inbound message.
//
// gorilla's SetReadLimit already caps this at the socket, so this is the second
// of two independent limits rather than the only one. Both are cheap; a decoder
// is the wrong place to discover that someone sent a 40MB JSON document.
const MaxCommandBytes = 4096

func newLimitedReader(b []byte) io.Reader {
	return io.LimitReader(bytes.NewReader(b), MaxCommandBytes)
}
