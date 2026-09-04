# RUNBOOK

Everything below was executed against this tree on **2026-09-03**, Go 1.26.2,
linux/arm64. Nothing here was written from reading source. Where a flag you
might expect does not exist, that is stated rather than invented.

The one rule this file exists to enforce: **you should be able to produce the
workshop visuals from a cold start without opening a source file.**

---

## 0. Cold start

```
cd projects/open-outcry
go build ./...          # ~10s cold, then cached
go test ./...           # 11 packages, all green, ~11s
```

Verified output of `go test ./...`:

```
?   github.com/ziaulalam1/open-outcry              [no test files]
ok  github.com/ziaulalam1/open-outcry/cmd/open-outcry     6.555s
ok  github.com/ziaulalam1/open-outcry/cmd/swarm           2.074s
ok  github.com/ziaulalam1/open-outcry/internal/arch       1.052s
ok  github.com/ziaulalam1/open-outcry/internal/chaos      0.879s
ok  github.com/ziaulalam1/open-outcry/internal/engine     0.320s
?   github.com/ziaulalam1/open-outcry/internal/hub        [no test files]
ok  github.com/ziaulalam1/open-outcry/internal/invariant  0.004s
ok  github.com/ziaulalam1/open-outcry/internal/loop       0.177s
?   github.com/ziaulalam1/open-outcry/internal/seed       [no test files]
ok  github.com/ziaulalam1/open-outcry/internal/wire       0.004s
```

`gofmt -l .` prints nothing. `go vet ./...` exits 0.

### The clone gate — run this before any talk, and after any `.gitignore` edit

Everything above compiles and tests the **working tree**. None of it can tell
you what is actually in the repository. On 2026-09-04 that gap turned out to be
hiding the fact that `.gitignore` had excluded both `cmd/` packages since the
first commit, so the published repo had no main package while every local check
stayed green (`docs/build-log.md` entry 16).

```
git clone https://github.com/ziaulalam1/open-outcry.git /tmp/oo-clonetest
cd /tmp/oo-clonetest
ls cmd/open-outcry cmd/swarm      # both must exist
go build ./... && go test ./...   # must be clean, 11 packages
go run ./cmd/open-outcry -port 8099
```

Verified from a real clone on 2026-09-04: both packages present, build clean,
11 packages green, server serving, and a 20s swarm run at `-rate 30` reporting
0 errors, 0 abnormal closures, backpressure 1386.

**If what you ship is a repository, the test starts with `git clone`.**

---

## 1. The server

**Binary:** `cmd/open-outcry`. Everything is served from one process — both
views, the WebSocket hub, the chaos control surface, and the static assets,
which are embedded in the binary via `embed.go`. There is no separate web
server and no build step for the frontend.

```
go run ./cmd/open-outcry -port 8080
```

or build it first, which is what you want on the day:

```
go build -o /tmp/open-outcry ./cmd/open-outcry
/tmp/open-outcry -port 8080
```

### Flags — the complete list

Verified by running the binary with `-h`:

```
Usage of open-outcry:
  -port int
        listen port (default 8080)
```

**That is the entire flag surface.** One flag. `main.go` says so in a comment
("the only configuration in this repository") and `-h` confirms it. There are
no `-seed`, `-chaos`, `-hero`, or `-rcvbuf` flags on the server — chaos is
driven over HTTP at runtime (§3) and the view options are URL query params (§2).

**There are no environment variables.** `grep -rn "os.Getenv\|LookupEnv"
--include=*.go .` returns nothing. Configuration is the one flag plus the query
string, and nothing else.

### What it prints on start

```
2026/09/03 20:59:58 open-outcry listening on :8080
2026/09/03 20:59:58   projector  http://192.168.215.2:8080/?view=projector
2026/09/03 20:59:58   trader     http://192.168.215.2:8080/?view=trader
2026/09/03 20:59:58   chaos      http://192.168.215.2:8080/chaos?armed=1&delay=1200&drop=3
```

The IP is discovered from the machine's non-loopback interfaces, not
hard-coded. **This is the address to read off the screen if the QR fails** —
it is the one thing in the room that knows which interface the laptop is on.

Health check: `curl -s http://localhost:8080/healthz` → `ok` (HTTP 200).

Shutdown: `^C` once drains connections (5s budget) and logs `clean shutdown`.
A second `^C` kills it. If connections do not drain, it dumps goroutine stacks
to stderr rather than exiting silently.

---

## 2. URLs

### Projector

```
http://localhost:8080/?view=projector
```

`view=projector` is also the **default** — a bare `http://localhost:8080/`
mounts the projector. Query params, all verified by rendering the page in
headless Chromium and inspecting the resulting DOM:

| Param | Values | Verified effect |
|---|---|---|
| `view` | `projector` \| `trader` | Selects the view. Default `projector`. |
| `hero` | `1` or `true` | Adds `hero` to `<body>`, sets `--s: 1.3`. **+30% type size.** Confirmed: `--s` read back as `1.3` vs `1`. |
| `grid` | `0` | Hides the room grid. Confirmed: 24 grid cells → 0. Any other value (or absent) leaves it on. |
| `idle` | `1` | Pins the "SCAN TO TRADE" QR screen up permanently, even after book frames arrive. Confirmed by screenshot. |
| `fixture` | `1` \| `main` \| `halt` | Replays a recorded tape. **No WebSocket is opened.** `1` and `main` load `/fixtures/tape.json`; `halt` loads `/fixtures/tape-halt.json`. |
| `speed` | number | Fixture playback multiplier. Only meaningful with `fixture`. |

There is **no `chaos` query param and no `split` query param.** The split is
reached over HTTP (§3), not by URL.

### Trader

```
http://localhost:8080/?view=trader
```

This is what a phone gets. Verified rendering: a session id in the header
(three characters, e.g. `6e3`), live BID / SPREAD / ASK, a size row
(`50 100 250 500`), and preset price buttons. No free-text entry anywhere —
that was a deliberate choice so a person at the back of a room can trade
one-handed.

The trader view accepts `fixture` too (it falls back to the tape's top-of-book),
but takes none of the projector's display params.

### Keys — on the projector page only

| Key | Effect | Verified |
|---|---|---|
| **Space** | **Freeze / unfreeze.** Toggles `frozen` on `<body>`. | Yes — `bodyClass` read back as `frozen`. |
| **h** or **H** | Toggle hero mode (same as `hero=1`). | Yes — `bodyClass` → `hero`, `--s` → `1.3`. |

Freeze is **rendering only**. The socket keeps reading, the engine keeps
matching, the sequence number keeps advancing. Unfreeze and the book has jumped
forward — which is the point of the gesture, not a side effect of it.

These are the only two key bindings in the tree. `grep -rn "keydown" web/`
returns exactly one handler.

---

## 3. Chaos, and reaching the split on demand

The presenter's control surface is an HTTP endpoint, so it is reachable from a
phone while you are standing up.

```
curl -s "http://localhost:8080/chaos?armed=1&delay=1200&drop=3"
```

Verified response:

```json
{"armed":true,"delay_ms":1200,"drop_every":3}
```

| Param | Meaning |
|---|---|
| `armed` | `1`/`true` to arm, anything else disarms |
| `delay` | injected delay in **milliseconds** |
| `drop` | drop one frame in every N |

Every param is optional and omitted params keep their current value, so
`curl "http://localhost:8080/chaos"` with no query string is a **read-only
snapshot** — verified: it returned the armed state unchanged. Disarm with
`?armed=0`.

### What actually latches the split

`internal/hub/hub.go:191` sets `Split: h.chaos.armed`. So:

> **The split IS "chaos armed."** Arming chaos flips the projector into the
> two-pane layout on the next stats frame. There is nothing else to press.

But an armed split with no traffic is two panes showing the same seed book.
**Divergence needs order flow** — the degraded pane can only fall behind if
frames are being produced for it to fall behind on. That is what §4 is for.

Measured divergence, chaos at `delay=1200&drop=3`, swarm at `-rate 30`:

| elapsed | reported lag |
|---|---|
| ~15s | 13.1s behind |
| ~30s | 16.8s behind |
| ~60s | 25.3s behind |
| ~90s | 33.8s behind |

**The lag grows without bound while the swarm runs.** If you want a number that
matches the talk's "about a second and a half behind" framing, shoot early or
run a lower rate. If you want the largest, most dramatic number on screen,
let it run. Both are honest; pick before you shoot rather than during.

---

## 4. `cmd/swarm`

Drives N real WebSocket connections. It has no access to the engine and no
special privileges — it is indistinguishable from that many phones.

```
go run ./cmd/swarm -n 24 -rate 30 -dur 45s -blackhole 1
```

### Flags — the complete list

Verified by `-h`:

```
  -addr string      server host:port (default "localhost:8080")
  -blackhole int    how many clients never read their socket (default 1)
  -dur duration     how long to run (default 1m0s)
  -mid int          reference price in cents (default 10250)
  -n int            number of simulated attendees (default 24)
  -rate float       orders per second, per client (default 0.7)
  -seed int         random seed; the same seed replays the same room (default 1)
```

**Client count is `-n`.** That is the flag you asked for.

### Profiles — there is no flag for these

There are four behaviour profiles — `rester`, `crosser`, `canceller`,
`blackhole` — and **none of them is settable from the command line.** The mix
is assigned structurally in `assignProfiles()`:

- clients are dealt round-robin by `i % 5`: two resters, two crossers, one
  canceller per group of five;
- then `-blackhole N` **overwrites the last N slots**, so asking for a
  blackhole always gets one even at small `-n`.

So `-blackhole` is the only lever on the mix. The ratio of rester to crosser to
canceller is fixed in code. Verified at `-n 24 -rate 30 -dur 120s -blackhole 1`,
chaos armed:

```
ran 2m0.005s
profile      clients      sent       read  errors  closed
rester            10     36000     894235       0       0
crosser            9     32398     811859       0       0
canceller          4     14399     348907       0       0
blackhole          1       257          0       0       1
total             24     83054    2055001       0       1

1 blackhole disconnect(s): the server timed them out because they never
pong (pongWait). That is the design working, not an error.

server counters, read off the stats feed over this run:
  backpressure                   1407   (projector: "dropped · slow phone")
  chaos dropped                 82679   (projector: "dropped · chaos")
  engine seq                    83001   481 stats samples
  split                          true   chaos armed, delay 1200ms

the blackholed client's send buffers DID overflow: 1407 frames dropped and
counted. This is the number act two points at.
```

### Reading that summary

- **`errors` is malfunction. `closed` is the server hanging up.** They are
  separate columns because they mean opposite things. **A clean run reports
  zero in `errors`.** If it does not, something is actually wrong.
- **One `closed` on the blackhole is expected on every run.** It never reads,
  so it never pongs, so the server's `pongWait` (25s) expires and the server
  disconnects it. That is the design working. Prior to 2026-09-04 this printed
  as an error and made every correct run look like a failed one.
- **The blackhole reading 0 frames** is the load-bearing line. The binary
  prints a WARNING if a blackholed client read anything, because a blackhole
  that reads is not exercising the drop counter at all.
- **The `server counters` block is measured, not inferred.** `cmd/swarm` holds
  one extra connection on the stats feed for the run, takes a baseline before
  the load starts, and reports deltas. If that connection fails it prints
  **`NOT OBSERVED`** rather than `0` — a zero meaning "not measured" and a zero
  meaning "nothing was dropped" are opposite findings that look identical.
- The observer connection means the server's own client count is `-n` **plus
  one**. The summary prints the server's figure rather than assuming.

The room grid on the projector populates from these clients: `-n 24` produced
exactly **24 grid cells**, confirmed by DOM inspection.

---

## 5. The loopback drop-counter caveat — read this before you debug anything

On localhost the projector's **`dropped · slow phone`** counter can read **0**
even with a blackholed client connected and everything working correctly. The
kernel auto-tunes loopback socket buffers into the megabytes, so a client that
has stopped reading absorbs an enormous amount before the server's send buffer
overflows and anything gets counted.

**There is no flag or environment variable to shrink `SO_RCVBUF` for a local
run.** This is not an oversight and you should not go looking for it:

- `grep -rni "rcvbuf\|SetReadBuffer" --include=*.go .` → **no matches.**
- `grep -rn "os.Getenv\|LookupEnv" --include=*.go .` → **no matches.**
- The server's `-h` lists `-port` and nothing else.

Such a flag **was built in a previous session, measured, and deliberately
deleted**, because the measurement showed it does nothing. Reproduced today,
one fresh server process per condition:

| condition (fresh server each) | `dropped · slow phone` |
|---|---|
| `-rate 6 -dur 45s`, chaos armed | **0** |
| `-rate 30 -dur 45s`, chaos armed | **1,387** |

Prior session measured 0 and ~1,352 for the same two conditions. Today: 0 and
1,387. **Throughput is the variable. The socket buffer is not.**

### So: how to make the counter move during rehearsal

Raise the rate. That is the whole answer.

```
go run ./cmd/swarm -n 24 -rate 30 -dur 45s -blackhole 1
```

**You no longer have to read the projector to check.** Since 2026-09-04 the
swarm subscribes to the stats feed itself and prints the measured counter at the
end of every run, as a delta over that run:

```
server counters, read off the stats feed over this run:
  backpressure                   1407   (projector: "dropped · slow phone")
  chaos dropped                 82679   (projector: "dropped · chaos")
```

and then says, in words, whether the blackhole's buffers overflowed or whether
the counter sat at zero and why. If backpressure is 0 it prints the loopback
explanation and tells you to raise `-rate`, rather than leaving you to wonder.

Before that change the summary asserted the buffers "should have overflowed and
been counted" without subscribing to anything that could tell it. See
`docs/build-log.md` entry 15.

The swarm binary prints this warning itself whenever you point it at localhost
with `-rate` under 20 and at least one blackhole:

```
swarm: NOTE - on localhost at this rate the projector's "dropped - slow phone"
       counter will read 0. It is not broken: the kernel auto-tunes loopback
       buffers, so a non-reading client absorbs megabytes before anything
       backs up. Measured: 0 drops at -rate 6, ~1340 at -rate 30 over 45s.
       For rehearsal use -rate 30 -dur 45s. On venue wifi it moves on its own.
```

If you see that message, the software is fine. **On venue wifi the counter
moves on its own** — real phones on one access point are genuinely slow readers
in a way loopback never is. Do not debug this on the day.

Note the two counters are different things and only one of them is affected:

- **`dropped · slow phone`** — backpressure. Frames dropped because a client
  could not keep up. This is the one that reads 0 on loopback at low rates.
- **`dropped · chaos`** — frames the chaos layer dropped on purpose
  (`drop=3` → one in three). This moves immediately at any rate, on any
  network, because it is deterministic and has nothing to do with sockets.

---

## 6. THE SEQUENCE — most photogenic screen, solo, from cold

Copy-paste, in this order. Three terminals. Measured end to end today; the
screenshot it produces is the one in §7.

```bash
# ── terminal 1 — the server
cd projects/open-outcry
go build -o /tmp/open-outcry ./cmd/open-outcry
/tmp/open-outcry -port 8080

# ── terminal 2 — arm chaos (this is what creates the split)
curl -s "http://localhost:8080/chaos?armed=1&delay=1200&drop=3"
#   expect: {"armed":true,"delay_ms":1200,"drop_every":3}

# ── terminal 3 — populate the room grid AND move the drop counter
cd projects/open-outcry
go run ./cmd/swarm -n 24 -rate 30 -dur 120s -blackhole 1
```

Then, in the browser — **fullscreen it (F11), 1920×1080 or the projector's
native resolution**:

```
http://localhost:8080/?view=projector&hero=1
```

Then, at the keyboard:

1. **Wait 20–30 seconds.** Watch the `behind by` figure climb. Divergence is
   what makes the two panes photograph differently; at t=0 they are identical
   and the picture says nothing.
2. **Press `Space`** to freeze. The screen holds. Nothing on it will change
   while you compose the shot, walk to the side of the screen, or take a burst.
3. Shoot.
4. **Press `Space` again** to unfreeze — the book jumps forward, which is the
   live proof that freeze was a rendering pause and not a stopped system.

`-dur 120s` gives you two minutes of populated grid to work in. Raise it if you
are rehearsing repeatedly; the swarm exits on its own and the grid empties when
it does.

Why each element is in the sequence:

| Element | Why it is there |
|---|---|
| `chaos?armed=1` | The **only** thing that creates the split. Without it there is one pane. |
| `swarm -n 24` | Populates the room grid with 24 cells. Without it the grid is empty and the screen looks like a mockup. |
| `-rate 30` | Makes `dropped · slow phone` non-zero on localhost (1,387 measured). At the default 0.7 it reads 0 and the frame has a zero in it. |
| `-blackhole 1` | The non-reading client that generates the backpressure in the first place. |
| `hero=1` | +30% type, so the numbers are legible in a photo taken from the back of a room. |
| Wait 20–30s | Lets the panes visibly diverge. |
| `Space` | Freezes rendering so a burst of frames are all identical and all sharp. |

---

## 7. What that produces

Captured today at 1920×1080, hero on, chaos armed, swarm at rate 30:

- Headline: `OPEN OUTCRY` · `LIVE` · `11,655 ORDERS`
- Banner: *"The right-hand book is 16.8 seconds behind. 11,047 updates never
  arrived. Both books are correct — only delivery degraded."*
- Left pane `LIVE FEED seq 11,701`, right pane `DEGRADED FEED seq 14 · behind
  by 16.8s / 11,687 frames`, price ladders diverging with per-level deltas
- Invariant row, all four green: `✓ CONSERVATION` `✓ NO CROSSED BOOK`
  `✓ PRICE-TIME PRIORITY` `✓ SWEEP COMPLETE`
- Counter row: `1,393 dropped · slow phone` · `11,047 dropped · chaos` ·
  `1,200ms injected delay` · `60 goroutines`
- Room grid across the bottom, 24 cells

The invariants staying green while the delivery counters climb is the entire
argument of act two, and it is on screen simultaneously with the damage. That
is the frame worth photographing.

---

## 8. Fallbacks

**No network, no laptop-to-phone path, or you just want the screen alone:**

```
http://localhost:8080/?view=projector&fixture=1
```

Replays a recorded tape against a synthetic clock, opens no WebSocket, and
loops forever so an unattended projector never goes blank. Verified: renders a
book, `LIVE 15 ORDERS`, no split.

To rehearse the halt path: `?view=projector&fixture=halt`.

**Careful — the fixture tape carries a roster of 29 attendees**, so the room
grid populates from the tape rather than from real clients. That is exactly the
discrepancy recorded as build-log entry 13: the grid worked against the fixture
and was empty against the real server, because production has no roster —
nobody logs in. If you are demonstrating the room grid, the fixture is lying to
you in your favour. Use the swarm.

**Slow down a fixture for a steadier shot:** `&fixture=1&speed=0.5`.

**QR won't scan / no phone reaches the laptop:** read the LAN URL off the
server's own startup log (§1) and put it on a slide. `?idle=1` pins the QR
screen up with the URL printed under it in plain text.

---

## 9. Not verified in this pass

Stated so nobody mistakes silence for confirmation.

- **Real phones on real venue wifi.** Everything above ran on loopback in a
  Linux container. The claim that the drop counter "moves on its own" on venue
  wifi is inherited from the prior session's reasoning and from the kernel
  behaviour described in §5 — it has **not** been measured against actual
  phones on an actual access point.
- **The QR code scanning from a phone camera.** The QR renders (screenshot
  confirmed) and encodes the LAN URL. Whether a given phone reads it at a given
  distance and projector brightness is a rehearsal item. See
  `docs/jsqr-v23-alignment.md` for why QR decoding is not assumed to be a
  solved problem here.
- **Projector colour and contrast.** The near-black background is a design
  choice for photography; how it survives a specific venue's projector is
  unmeasured.
- **`-mid` and `-seed`** on the swarm were not varied. They are listed because
  `-h` lists them, not because their effect was exercised.
