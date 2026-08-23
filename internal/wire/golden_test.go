package wire

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"github.com/ziaulalam1/open-outcry/internal/engine"
	"github.com/ziaulalam1/open-outcry/internal/seed"
)

var update = flag.Bool("update", false, "rewrite the golden files")

// ── the round trip that retires the duplication ─────────────────────────────
//
// cmd/swarm encodes wire.Command; the server decodes wire.Command. Through
// checkpoint 3 swarm carried its own copy of those fields as deliberate debt.
// This test is the payoff, and it is what makes the single definition load
// bearing rather than merely tidy: if encode and decode ever disagree, this
// fails here rather than as an order that silently does nothing on stage.

func TestCommandRoundTrip(t *testing.T) {
	cases := []struct {
		name string
		cmd  Command
		want engine.Command
	}{
		{"buy", SubmitCmd("buy", 10250, 100), engine.Submit{Session: "abc", Side: engine.Buy, Price: 10250, Qty: 100}},
		{"sell", SubmitCmd("sell", 10275, 50), engine.Submit{Session: "abc", Side: engine.Sell, Price: 10275, Qty: 50}},
		{"cancel", CancelCmd(42), engine.Cancel{Session: "abc", ID: 42}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := DecodeCommand("abc", c.cmd.Encode())
			if err != nil {
				t.Fatalf("decode(%s): %v", c.cmd.Encode(), err)
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("round trip produced %#v, want %#v", got, c.want)
			}
		})
	}
}

// The session comes from the CONNECTION, never from the payload. Without this,
// any phone could cancel anyone's order by guessing an id.
func TestDecodeIgnoresAnySessionInThePayload(t *testing.T) {
	raw := []byte(`{"type":"cancel","id":7,"session":"victim"}`)
	_, err := DecodeCommand("attacker", raw)
	if err == nil {
		t.Fatal("a payload carrying its own session must be rejected, not honoured")
	}
}

func TestDecodeRejectsHostileInput(t *testing.T) {
	for _, tc := range []struct{ name, raw string }{
		{"not json", `{`},
		{"unknown type", `{"type":"withdraw"}`},
		{"bad side", `{"type":"submit","side":"sideways","price":1,"qty":1}`},
		{"zero qty", `{"type":"submit","side":"buy","price":10250,"qty":0}`},
		{"negative price", `{"type":"submit","side":"buy","price":-5,"qty":10}`},
		{"absurd qty", `{"type":"submit","side":"buy","price":10250,"qty":999999999}`},
		{"absurd price", `{"type":"submit","side":"buy","price":999999999999,"qty":10}`},
		{"cancel without id", `{"type":"cancel"}`},
		{"unknown field", `{"type":"submit","side":"buy","price":1,"qty":1,"leverage":100}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := DecodeCommand("s", []byte(tc.raw)); err == nil {
				t.Fatalf("accepted %s", tc.raw)
			}
		})
	}
}

// ── the schema contract with the view that was built first ──────────────────

// The projector was built months of decisions before this encoder existed, from
// a hand-authored tape. This asserts the encoder still emits everything that
// tape taught the view to read. A field the projector reads but the server never
// sends is a test failure here, rather than a blank space discovered on stage.
func TestBookFrameSatisfiesTheProjectorsTape(t *testing.T) {
	tapePath := filepath.Join("..", "..", "web", "fixtures", "tape.json")
	raw, err := os.ReadFile(tapePath)
	if err != nil {
		t.Skipf("tape not readable: %v", err)
	}
	var tape struct {
		Frames []map[string]json.RawMessage `json:"frames"`
	}
	if err := json.Unmarshal(raw, &tape); err != nil {
		t.Fatalf("tape: %v", err)
	}

	var want map[string]json.RawMessage
	for _, f := range tape.Frames {
		var typ string
		if json.Unmarshal(f["type"], &typ) == nil && typ == "book" {
			want = f
			break
		}
	}
	if want == nil {
		t.Fatal("no book frame in the tape")
	}

	var got map[string]json.RawMessage
	if err := json.Unmarshal(liveBookFrame(), &got); err != nil {
		t.Fatal(err)
	}

	// at_ms is playback time and exists only in the tape: the live path carries
	// as_of_ms, which is production time stamped upstream of the chaos line.
	skip := map[string]bool{"at_ms": true}
	var missing []string
	for k := range want {
		if !skip[k] && got[k] == nil {
			missing = append(missing, k)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Fatalf("the encoder omits fields the projector reads: %v", missing)
	}

	// The reverse direction is a warning, not a failure: the server may send more
	// than the current view consumes.
	for k := range got {
		if want[k] == nil && k != "as_of_ms" {
			t.Logf("note: encoder emits %q, which the tape does not exercise", k)
		}
	}
}

// ── golden ──────────────────────────────────────────────────────────────────

// The seeded opening book, encoded, byte for byte. Deterministic because the
// seed is deterministic and the engine has no clock.
func TestSeededBookGolden(t *testing.T) {
	got := liveBookFrame()
	path := filepath.Join("testdata", "seed_book.golden.json")

	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("wrote %s (%d bytes)", path, len(got))
		return
	}

	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run: go test ./internal/wire -update)", err)
	}
	if string(got) != string(want) {
		t.Fatalf("encoded opening book changed.\n got: %s\nwant: %s", got, want)
	}
}

func liveBookFrame() []byte {
	book := engine.New(engine.Config{Depth: 8})
	var res engine.Result
	for _, c := range seed.Baseline() {
		res = book.Apply(c)
	}
	return New().Book("fresh", res, seed.Session, 0, false)
}
