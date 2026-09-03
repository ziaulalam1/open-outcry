# open-outcry

A live order book you run in a room. Thirty people open a URL on their phones,
send orders, and watch a matching engine build a book on a projector in front of
them. Then delivery is deliberately broken while the book stays provably
correct, and the room is asked what a trading system should show a user whose
prices are twelve seconds stale.

Go, vanilla JavaScript, no framework, no build step, one binary.

```
go run ./cmd/open-outcry -port 8080
```

Then open `http://localhost:8080/?view=projector`.

For anything operational — flags, URLs, how to reach the split, how to get the
screen into its most photogenic state — see **[docs/RUNBOOK.md](docs/RUNBOOK.md)**.
Every command in it was verified by execution.

---

## What is here

| | |
|---|---|
| 6,323 lines of Go | 79 test and fuzz functions, 11 packages |
| Matching engine | price-time priority, deterministic, clockless |
| Invariant checker | separate package, imports nothing from this module |
| Chaos layer | injected delay and frame drops, runtime-controlled over HTTP |
| WebSocket hub | fan-out with per-topic retention for gapless late joins |
| Load client | `cmd/swarm`, N real sockets, indistinguishable from N phones |
| Two views | projector and trader, served from the same binary via `go:embed` |

---

## Architecture

```
phones ──ws──┐
             ├──► hub ──► loop ──► engine        (single owner, no locks)
projector ───┘      ▲       │
                    │       ├──► invariant       (audits, cannot see internals)
                    └───────┴──► chaos ──► stale pane
```

The dependency rule is the design, and it is enforced by tests rather than by
convention. `internal/arch/arch_test.go` fails the build if any of it drifts:

- `TestImportBoundaries` — every internal package has a declared allowlist.
- `TestEngineDependsOnAlmostNothing` — the engine imports no hub, no transport,
  no `context`, no clock.
- `TestJSONIsConfinedToTheWirePackage` — `encoding/json` appears in exactly one
  package.
- `TestEveryInternalPackageHasARule` — a new package cannot be added without
  someone deciding what it is allowed to import.

The engine does not know a hub exists. The hub cannot name a domain type. The
chaos layer's only outward reference is a function value. `cmd/open-outcry/main.go`
is the one file that sees every package at once — the composition root, and the
only place the wiring exists.

### The concurrency design, and why it is this one

**Exactly one goroutine in the process holds the `*engine.Book` pointer.**
Commands arrive as values on a channel; results leave as bytes. No other
goroutine can reach book state.

**There are no locks anywhere in this repository.** That is not a stunt. "No
data races" is a claim about *ownership* here, not about correct locking — and
ownership is checkable by reading one file, whereas correct locking is checkable
only by reasoning about every file at once. A mutex-guarded book would have been
fewer lines and would have made every future change a question about lock
discipline. The single-owner loop makes the same guarantee structurally, and the
guarantee survives someone who has not read the code.

It also buys the thing act three needs: the engine is **deterministic and
clockless**, so the command log fully determines the book state. Replay is a
property of the engine, not of any infrastructure in front of it.

Three consequences worth stating because they cut against the design:

- **Inbound and outbound have opposite drop policies, on purpose.** Outbound:
  drop the frame and count it. Inbound: reject and tell the trader
  (`ErrBusy`). A trader shown "submitted" for an order that never reached the
  book is a correctness bug wearing a delivery bug's clothes, and it would
  destroy the exact claim act two exists to make.
- **Frame type is part of the hub topic.** The hub retains one frame per topic
  so a late joiner gets a book immediately. Publishing a halt onto the book
  topic would evict the book and leave the joiner with a banner over an empty
  ladder.
- **The projector opens two WebSockets, not one multiplexed connection.** A
  single connection cannot have two writers, and multiplexing would
  head-of-line-block the fresh pane behind the stale pane's delay ring —
  degrading the control exactly as much as the experiment and destroying the
  comparison the whole demo rests on.

### The invariant checker

`internal/invariant` **imports nothing from this module.** It cannot name
`engine.Book`, cannot reach an unexported field, and cannot be handed a
precomputed total. It walks the book to derive what it holds, folds a ledger
from the events the engine *said* it emitted, and compares the two.

The reasoning: if conservation were checked against a running counter the
matcher maintains, a wrong decrement would move both sides of the equation
together and the check would pass vacuously — staying green through exactly the
class of bug it exists to catch.

> Agreement between two independently produced representations is evidence.
> Agreement between a value and itself is not.

Four checks render on the projector as a live row: conservation, no crossed
book, price-time priority, sweep complete.

---

## Running the three acts

Full commands in [docs/RUNBOOK.md](docs/RUNBOOK.md) §6. In outline:

**Act one — it works.** Start the server, project
`/?view=projector`, put the QR on screen. People scan, tap a size and a price,
and the book builds. Price-time priority stops being a definition the moment
someone watches their own order sit behind an earlier one at the same price.

**Act two — I break it.** Arm chaos:

```
curl -s "http://localhost:8080/chaos?armed=1&delay=1200&drop=3"
```

The screen splits into `LIVE FEED` and `DEGRADED FEED`. The right-hand book
falls behind and the banner counts how far. The invariant row stays green
throughout — both books are correct, only delivery degraded. Then the question
to the room: *these people are about to trade on prices that no longer exist.
Do you show them stale data, or nothing?*

**Act three — the trade-off, and what I got wrong.** Three ways a measuring
instrument lied during this build, all found by accident: a decoder with a bad
lookup table, an architecture test that printed PASS while dead, and a fixture
more generous than production could ever be. Entries 5, 10 and 13 of
[docs/build-log.md](docs/build-log.md).

Rehearsing alone, with no audience, is `cmd/swarm` — see the runbook.

---

## Limitations

Written unprompted, because a project that lists no limitations is either
trivial or under-examined, and because every item here is a question someone
will ask.

### What the invariant checker cannot catch

Its independence is real but bounded. It is a *consistency* checker, not a
correctness oracle.

- **It cannot catch a wrong-but-consistent match.** If the engine fills the
  wrong resting order at the correct price and quantity, conservation still
  balances, the book is still uncrossed, and the ledger still agrees with the
  walk. Only `TestPriority`-style unit tests catch that class, and only for the
  cases someone thought to write.
- **It shares the engine's notion of an event.** The ledger is folded from
  emitted events. An event the engine *fails to emit* is invisible to both
  representations — the book will not contain the effect and neither will the
  ledger, and they will agree. Independence in the import graph does not buy
  independence from a shared blind spot in what counts as an observation.
- **It is a point-in-time check, not a temporal one.** It says the book is
  consistent now. It says nothing about the *path* — an ordering violation that
  self-heals before the next check leaves no trace.
- **It checks the book, not the transport.** Everything act two breaks is
  invisible to it by design. That is why the invariant row stays green while
  the drop counters climb, which is the point — but it means the checker is
  silent on the entire class of failure the workshop is about.
- **Fuzzing is shallow.** 79 test and fuzz functions is decent coverage of the
  cases considered; it is not a proof, and the fuzz corpus is small.

### Conflation is the production answer, and it is deliberately not shipped

The honest answer to act two's question — what a real venue does with a slow
consumer — is **conflation**: stop queueing every update and instead keep only
the latest state per instrument, so a slow client receives fewer, newer frames
rather than a growing backlog of stale ones. It is what commercial market data
systems do. It is not hard to implement here.

It is not shipped, and that is a choice rather than an omission:

- Conflation makes the split **less visible**. The degraded pane would stay
  roughly current and merely lose detail, which is better engineering and a
  worse demonstration. The pane falling twelve seconds behind is the thing you
  can see from the back of a room.
- Shipping the fix converts an open question into a solved one. The workshop's
  value is the room arguing about stale-versus-nothing; handing them the
  industry answer in the first thirty seconds ends the argument.
- The trade-off is the content. A demo that quietly implements the right answer
  teaches nobody why it is the right answer.

So: the drop policy here is drop-and-count, chosen for legibility. If this were
production, it would be conflation with a per-topic latest-value cache, and the
counter would measure conflated updates rather than lost ones. Stated here so
nobody has to catch me on it.

### What breaks at 10⁶ msg/s

Nothing in this repository has been run near that, and the design would fail
well before it. The specific failure points, in the order they would arrive:

1. **The single-owner command channel becomes the ceiling first.** One goroutine
   applying every command serialises all order flow. The exact ceiling is
   unmeasured; the structure guarantees there is one, and it is the same
   structure that buys the no-locks guarantee. At 10⁶/s the design trade
   inverts — you would shard the book by instrument and accept per-shard
   ownership, which is the standard answer and a different program.
2. **JSON encoding, per frame, per client.** Frames are encoded in
   `internal/wire` with `encoding/json` and fanned out. At high fan-out this is
   the dominant cost long before matching is. A binary framing with a shared
   encoded buffer per topic is the obvious fix and is not here.
3. **Fan-out is O(clients) per frame.** The hub writes to every subscriber's
   channel. Thirty phones is nothing; thirty thousand is a different
   architecture with a tree of relays.
4. **Per-connection goroutines.** Two per connection is fine at room scale and
   is not how you serve six figures of concurrent sockets.
5. **`int64` cents.** Prices are integer cents, which is correct and
   overflow-safe at any plausible price, but the aggregate quantity totals the
   invariant checker sums are not audited for overflow at extreme volumes.

The honest framing: this is a **teaching instrument built to be watched**, and
several decisions that are right for that are wrong for throughput. The
concurrency story is a claim about ownership and determinism, not about
performance, and no throughput number is claimed anywhere in this repository.

### Other things not claimed

- **No authentication, and `CheckOrigin` returns true unconditionally.** Phones
  reach the laptop over a venue LAN with no cookies and no auth, so there is
  nothing to hijack. This is documented at the call site so nobody "fixes" it
  into a same-origin check that blocks every phone in the room. It is not
  suitable for anything reachable from the internet.
- **Session ids are labels, not secrets** — three characters, sequential, no
  login. Anyone can claim any session.
- **Kafka ingest and OpenTelemetry are not built.** Planned; not here. Nothing
  in this repository claims them.
- **Verified on Linux/arm64 with Go 1.26.2.** Nothing platform-specific is
  known, and nothing else has been tested.

---

## Layout

```
cmd/open-outcry     composition root: the only file that sees every package
cmd/swarm           load client / rehearsal surface
internal/engine     matching, book, priority. Deterministic and clockless
internal/loop       owns the book. One goroutine, one channel, no locks
internal/invariant  audits the book. Imports nothing from this module
internal/chaos      injected delay and drops
internal/hub        WebSocket fan-out, per-topic retention
internal/wire       the only package that imports encoding/json
internal/arch       tests that fail the build when the boundaries drift
web/                projector and trader views, embedded into the binary
docs/               runbook, build log, slides, shot list, writeups
```

## Docs

| File | What it is |
|---|---|
| [docs/RUNBOOK.md](docs/RUNBOOK.md) | Every operational command, verified by execution |
| [docs/build-log.md](docs/build-log.md) | Dated entries, including the three instrument failures |
| [docs/jsqr-v23-alignment.md](docs/jsqr-v23-alignment.md) | A decoder bug derivation, self-contained |
| [docs/cfp-abstract.md](docs/cfp-abstract.md) | Submission draft |
| [docs/post-mortem.md](docs/post-mortem.md) | Written after the talk |

The slide deck and the photographer's shot list are written for whoever is
running the event rather than for a reader of this repository, and are kept out
of it on purpose.
