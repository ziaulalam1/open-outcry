package engine

import "testing"

// FuzzApply feeds arbitrary command streams at the book.
//
// The oracle is the invariant report itself — the same code that runs in the hot
// path on stage. That is the whole reason the checker is a pure function over a
// read-only view: it can grade a fuzz run without any test-only reimplementation
// of "is the book correct" to drift out of sync with the real one.
//
// Nothing here asserts what the engine SHOULD do with a given input. It asserts
// that whatever it does, the book stays consistent and it does not panic. Those
// are exactly the two properties a live demo needs.
func FuzzApply(f *testing.F) {
	f.Add([]byte{1, 40, 3, 0, 2, 60, 2, 1, 1, 50, 5, 0})
	f.Add([]byte{0, 0, 0, 0})
	f.Add([]byte{2, 255, 255, 1, 2, 0, 0, 0})

	f.Fuzz(func(t *testing.T, data []byte) {
		b := New(Config{Depth: 8})
		var live []OrderID
		owners := map[OrderID]SessionID{}

		// Four bytes per command keeps the mapping simple enough that a failing
		// corpus entry is readable by hand.
		for i := 0; i+3 < len(data); i += 4 {
			op, a, c, d := data[i], data[i+1], data[i+2], data[i+3]

			if op%8 == 7 && len(live) > 0 {
				k := int(a) % len(live)
				id := live[k]
				live = append(live[:k], live[k+1:]...)
				r := b.Apply(Cancel{Session: owners[id], ID: id})
				checkFuzz(t, r, data)
				continue
			}

			side := Buy
			if op%2 == 1 {
				side = Sell
			}
			sess := SessionID([]byte{'s', 'a' + (c % 8)})
			// Deliberately allows zero and very large quantities so the reject
			// paths get exercised too.
			cmd := Submit{
				Session: sess,
				Side:    side,
				Price:   Ticks(10200 + int64(a)),
				Qty:     Qty(int64(d) * 10),
			}
			r := b.Apply(cmd)
			checkFuzz(t, r, data)
			if x, ok := rested(r); ok {
				live = append(live, x.ID)
				owners[x.ID] = sess
			}
		}
	})
}

func checkFuzz(t *testing.T, r Result, data []byte) {
	t.Helper()
	if !r.OK() {
		t.Fatalf("invariant violated at seq %d: %s\ncorpus: %v", r.Seq, r.Report, data)
	}
	// A negative or absurd aggregate would slip past the identity if both sides
	// were wrong in the same direction, so assert the cheap sanity bounds too.
	for _, side := range [2]Side{Buy, Sell} {
		if r.Snapshot.Resting[side] < 0 || r.Snapshot.Filled[side] < 0 || r.Snapshot.Canceled[side] < 0 {
			t.Fatalf("negative aggregate at seq %d: %+v", r.Seq, r.Snapshot)
		}
	}
}
