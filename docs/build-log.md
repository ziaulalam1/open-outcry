# Build log — things that broke, and what they taught

Kept as they happened, not reconstructed afterwards. Entries are only added when
something actually failed — a log of successes would teach nothing.

Format: what broke · why it broke · what it changed · whether it is worth saying
out loud.

Four entries share a theme: **verification tooling needs verifying.** Four
distinct ways a measuring instrument lies, all found by accident in one build:

- **Entry 5 — the instrument was WRONG.** Five raster scales, three payloads,
  clean neighbours: a textbook bisection, and the conclusion was still false,
  because the one thing never put on trial was the decoder doing the measuring.
- **Entry 10 — the instrument was DEAD.** The architecture test's logic was
  correct throughout. Its *cache key* was wrong, so it replayed a stale green
  tick over a live violation. Nothing was measured; the answer came from memory.
- **Entry 13 — the instrument was more GENEROUS than production.** The room grid
  worked against the fixture and was empty against the server, because the tape
  carried a roster and production cannot have one — nobody logs in. This one
  cuts against the project's own fixture-first build order, which I would still
  choose.
- **Entry 14 — I built the broken instrument myself**, which makes it the
  strongest of the four: a cumulative counter read across successive trials,
  producing a dose-response curve out of nothing. It is the natural answer to
  *"how would you know?"*

A green check mark is a claim made by an entire pipeline — the assertion, the
instrument, and the reporting layer between them — and in this build each of
those failed at least once.

The wrong turns stay in the record. The reversals are the evidence; deleting
them would delete the proof that they were reversals.

---

## 1. The ladder rode up over the pane header, and clipped the best bid

*2026-08-23*

**Broke:** The first projector render put the top ask row underneath the pane's
own header. The collision landed on the best ask — the single number the room
actually watches.

**Why:** The ladder was a centred flex column with `flex: 1` and no
`overflow: hidden`, and I had hardcoded seven price levels per side. Seven asks
plus a spread row plus seven bids at that type size is taller than the box on a
1920×1080 screen. Overflowing flex content does not clip by default; it just
draws outside its parent.

**Fixed by:** `overflow: hidden` on the ladder — but that only converts an
overlap into a silent clip, which is not better. The real fix was to stop
hardcoding depth: the view now measures `scrollHeight - clientHeight` and drops
a level until the ladder fits, bounded between 7 and 3, re-fitted only when the
layout signature (viewport, hero mode, split) changes.

**Worth saying out loud:** yes, and it generalises. I do not know what
resolution the projector in the room will be. A hardcoded seven that happens to
fit my laptop is a demo that breaks on someone else's hardware, in front of
people. Measuring is three lines longer and cannot be wrong.

---

## 2. Hero mode reintroduced the same bug the same day

*2026-08-23*

**Broke:** After fixing #1 for the normal view, `&hero=true` overflowed by 130px.

**Why:** Hero mode scales all type by 1.3 via a CSS variable. I fixed the depth
for one scale factor and assumed it held for the other. It is the identical bug,
found again ninety seconds later, because the first fix treated a symptom
(*six levels fits*) rather than the cause (*depth must be derived, not chosen*).

**Fixed by:** the same auto-fit, with hero state as part of the layout key.

**Worth saying out loud:** yes — this is the more honest version of #1. The
first fix looked like it worked and was still wrong. That is what most
production bugs actually feel like.

---

## 3. The halt screen showed four green ticks while announcing a failure

*2026-08-23*

**Broke:** The halt fixture rendered `BOOK HALTED — CONSERVATION want 4,200 got
4,050` above an invariant strip reading `✓ CONSERVATION`. The screen contradicted
itself, in the exact frame that exists to be photographed.

**Why:** The strip read its report from the last book frame. A halt frame is not
a book frame, so the last *good* book's all-green report stayed on screen. The
precedence was written as `book.inv || halt.inv` — the stalest source winning.

**Fixed by:** inverting the precedence, with a comment saying why: the whole
point of halting is that the strip goes red, so a halt report must outrank the
report attached to the last frame that was still correct.

**Worth saying out loud:** yes, and it is the best one. The failure mode of a
correctness display is not that it fails to detect the problem — the checker
caught it fine. It is that the *reporting path* quietly disagrees with the
detector. An assertion nobody can see is not an assertion.

---

## 4. My first freeze test could only ever pass

*2026-08-23*

**Broke:** The spacebar-freeze test reported `HELD: true` and
`ENGINE_KEPT_RUNNING: false` and I nearly filed that as a code bug.

**Why:** The test pressed space fourteen seconds into a twenty-one second tape,
by which point playback had reached the final frame. Nothing more was going to
arrive, so "the engine kept running while the view was frozen" was trivially
unobservable. The test was measuring at the one moment the property is
unmeasurable.

**Fixed by:** moving the freeze to mid-tape, where frames remain, and asserting
both halves: the rendered sequence number must not move while held, and it must
jump forward on release.

**Worth saying out loud:** yes. A test that asserts the wrong thing is worse
than no test, because it produces a green tick you will trust later. This one
happened to fail loudly; the same mistake in the other direction would have
passed forever.

---

## 5. I diagnosed a QR bug in the wrong codebase, and nearly "fixed" correct code

*2026-08-23*

**Broke:** The hand-written QR encoder round-tripped 223 of 224 cases against an
independent decoder (jsQR, used only as a test oracle). The one failure was
version 23 at error-correction level L, at maximum data fill.

**What I concluded, and why it was reasonable:** I isolated it carefully first —
it failed at five raster scales, so not a resolution artifact; at three different
payloads, so not payload-dependent; and versions 21, 22, 24 and 25 all passed at
the same scale, so not a size limit. A defect isolated to exactly one
(version, ECC) cell, with its neighbours clean, is almost always one wrong number
in a lookup table. I wrote that up as an instruction to fix the table.

**What was actually true:** the wrong number was in the **oracle**. jsQR 1.4.0's
alignment-pattern table lists version 23 as `[6, 30, 54, 74, 102]`. The spec's
construction rule gives `[6, 30, 54, 78, 102]`. `74` is version 22's fourth
centre, duplicated one row down — a copy-paste slip in the decoder. jsQR samples
the symbol on a slightly wrong grid; at M, Q and H the extra redundancy absorbs
the resulting errors, and at L it cannot. Our encoder was right the whole time.

### The derivation, in full

ISO/IEC 18004 Annex E does not give alignment centres as free-form data; they are
*constructed*, which is what makes a single wrong entry provable rather than a
matter of whose table you trust. For version `v`:

```
size   = 4v + 17                     # modules per side
n      = floor(v / 7) + 2            # number of alignment tracks
first  = 6                           # pinned
last   = size - 7
step   = ceil((size - 13) / (2 * (n - 1))) * 2      # forced EVEN
tracks = [6] + [last - step*i  for i in n-2 .. 0]   # built DOWN from `last`
```

The two details that matter, and that a from-memory reconstruction gets wrong:
**the step is rounded up to an even number**, and **the sequence is built
downward from the last track** while the first stays pinned at 6 — so any
remainder is absorbed by the *first* gap, not distributed.

Version 23:

```
size = 4(23) + 17 = 109
n    = floor(23/7) + 2 = 3 + 2 = 5
last = 109 - 7 = 102
step = ceil((109 - 13) / (2 * 4)) * 2 = ceil(96/8) * 2 = 12 * 2 = 24
     -> 102, 78, 54, 30  (built downward), plus the pinned 6
     => [6, 30, 54, 78, 102]        gaps 24, 24, 24, 24
```

Version 22, which is where the bad value came from:

```
size = 105,  n = 5,  last = 98
step = ceil((105 - 13) / 8) * 2 = ceil(11.5) * 2 = 12 * 2 = 24
     -> 98, 74, 50, 26, plus 6
     => [6, 26, 50, 74, 98]         gaps 20, 24, 24, 24
```

So `74` is unambiguously v22's fourth track. jsQR lists v23 as
`[6, 30, 54, 74, 102]`, whose gaps are `24, 24, 20, 28` — a *non-uniform interior*,
which the construction above can never produce, because the interior tracks are
generated by repeated subtraction of a single constant. The remainder is only
ever allowed to land in the first gap. That is the tell: the defect is visible
from the shape of the row alone, without any reference table.

**Note for future-me:** the shortcut `(last - first) / (n - 1)` gives 24 for v23
and looks like it works. It is wrong in general — for v22 it gives 23, an odd
number, where the true step is 24. Do not reconstruct from the shortcut.

### The three independent confirmations

1. **Derivation from the construction rule** (above), done by hand, referring to
   no table. Gives `78`, and shows jsQR's row is structurally impossible.
2. **A second, unrelated encoder.** python-qrcode 8.2 encoding the same 1091-byte
   payload at v23/L produces a symbol differing from ours in **0 of 11,881
   modules** — byte-identical. And pristine jsQR *also* fails to decode
   python-qrcode's own v23/L symbol. Two independent encoders agreeing, and the
   decoder rejecting both, puts the fault on the decoder.
3. **The literal bytes in the shipped bundle.** Grepping
   `node_modules/jsqr/dist/jsQR.js` returns `[6, 30, 54, 74, 102]` directly. Not
   inferred — read.

A fourth, decisive check was available and was run: patching a *copy* of jsQR
with the single character `74` → `78` turns every v23/L case from "no QR found"
to an exact round-trip, and changes nothing else.

**Downstream decision:** we kept the spec-correct value. Bending the encoder to
satisfy a buggy decoder would produce symbols that real scanners — zxing, phone
cameras — misread. The test harness reports both tallies rather than one:
`223/224` against jsQR as published, `224/224` against jsQR with its one
character corrected. Printing only the second number would be a lie.

**Practical impact on this project: none.** The workshop URL is ~38 bytes, a
version 3 symbol, which round-trips on the pristine decoder at all four ECC
levels. This was never going to bite the demo. It would have bitten anyone who
reused the encoder.

**Worth saying out loud:** yes — this is the best entry in the file, and not
because of QR codes. Every step of my isolation was sound and the conclusion was
still wrong, because I never questioned the instrument. "The measurement
disagrees with the code" has two suspects and I only ever charged one of them.
The tell was available early and I walked past it: the failure was
error-correction-level dependent, and a *capacity or block-structure* error
cannot be ECC-dependent in that way — but a *sampling* error absorbed by
redundancy can be, exactly.

The part I would actually put on a slide: I then wrote the wrong diagnosis into
a task and handed it off. It came back refusing the premise, with the audit that
proved the encoder correct. Being wrong was cheap; being wrong *and
unfalsifiable* would not have been. Whatever you hand work to — a colleague, a
reviewer, a tool — has to be able to tell you your premise is wrong, and you
have to have asked in a way that lets it.

### Upstream status (checked 2026-08-23)

Filing an issue would be shouting into a void. Evidence, from the npm registry
and GitHub APIs rather than from a search engine:

| Signal | Value |
|---|---|
| Latest release | 1.4.0, **2021-04-24** (5 years old) |
| Last commit on `master` | **2021-08-24** |
| Open pull requests | 18 — oldest 2021-06, newest 2024-08, none merged |
| Open issues | 97 |
| Maintainer comments in the last 30 issue comments (2022-12 → 2025-07) | **0** |
| Repo archived? | No — so it *looks* maintained to anyone glancing at it |

The repository is not archived, which is the trap: nothing on the page says
"unmaintained", so the defect stays discoverable-but-unfixed and every new user
inherits it. The npm `modified` timestamp is recent for registry-metadata
reasons and says nothing about the code.

Conclusion: **dormant**. An issue is not worth writing; a public writeup is. The
derivation above is deliberately self-contained so it can be lifted into one
without rework — the spec construction, the two worked versions, the
non-uniform-interior tell, and three independent confirmations.

Note for anyone else hitting this: jsQR misreads only version 23 symbols, and
only at ECC level L, where there is not enough redundancy to absorb the
mis-sampling. At M, Q and H the same symbol decodes fine, which is why this can
sit undetected — most real payloads are far below version 23 anyway.

---

## 6. The auto-fit cached an answer for a pane that then got smaller

*2026-08-23*

**Broke:** After the auto-fit from #1 and #2 was working, the idle-state and hero
shots still overflowed — by 26px and 22px — while reporting the maximum depth.

**Why:** Two separate caching mistakes in the same six lines. First, the fit ran
on the very first render, when there was no book yet: an empty ladder trivially
"fits" at maximum depth, and that answer got cached and never revisited. Second,
the cache key was the viewport, hero flag and split flag — but the pane also
shrinks as the footer and the room grid fill up with live data. The fit was
correct for the taller pane it measured, and stale for the pane that existed a
second later.

**Fixed by:** refusing to cache a fit until there is content to measure, and
putting the pane's own measured height into the key.

**Worth saying out loud:** briefly. It is the classic cache-invalidation shape —
the key described the inputs I was thinking about rather than the inputs that
actually determined the answer.

---

## 7. I created my own race by parallelising work on one file

*2026-08-23*

**Broke:** A verification harness was rewritten underneath a running audit, so a
reported test tally did not match what the file on disk produced.

**Why:** I had two units of work running at once, both of which legitimately
needed to edit the same harness. Nobody did anything wrong locally; there was
simply no owner for that file.

**Worth saying out loud:** yes, and it lands nicely next to the architecture.
This whole project's answer to concurrency is *give every piece of mutable state
exactly one owner*. I then went and ran two jobs against one file with no owner
and got precisely the class of bug the design exists to prevent — not in the Go,
in how I ran the work. The principle does not stop being true when it is
inconvenient, and it applies above the code as well as inside it.

---

## 8. Playwright stalled screenshotting a pulsing dot

*2026-08-23*

**Broke:** The screenshot pass hung for thirty seconds and died on the hero shot,
intermittently.

**Why:** The live indicator is an infinite CSS animation. The screenshot call
waits for visual stability by default, and an animation that never ends never
becomes stable.

**Fixed by:** `animations: 'disabled'` on the screenshot call.

**Worth saying out loud:** probably not on stage — it is tooling trivia, not a
design lesson. Recorded because it cost real time and will recur the next time
anyone automates a shot of this page.

---

## 9. A guard that searched for a string the output can never contain

*2026-08-23*

**Broke:** The determinism test failed with "script does not exercise the
matcher", on a script that plainly does.

**Why:** The guard was `strings.Contains(stream, "Traded")`, over a stream built
with `fmt.Sprintf("%+v", events)`. `%+v` on a slice of interface values prints
each element's *fields* — `{Price:10250 Qty:100 ...}` — and never its type name.
The substring could not appear no matter what the engine did.

**Fixed by:** counting trades from the events directly instead of grepping a
rendered string.

**Worth saying out loud:** as a footnote to #4, yes. This one failed closed and
cost a minute. The same mistake in a guard phrased the other way round — assert
something absent rather than something present — passes silently forever. The
difference between the two is luck, not skill, which is the argument for
asserting on values rather than on their formatting.

---

## 10. The architecture test reported `ok (cached)` while the architecture was violated

*2026-08-23*

**Broke:** `internal/arch` is the file that turns "the engine knows nothing about
transport" from a comment into a build failure. To prove it actually fires, I
planted `import "encoding/json"` and `import "time"` in the engine and ran it.
It printed `ok (cached)`.

**Why:** The test reaches its conclusions by shelling out to `go list`. Go's test
cache keys on the files the test binary *opens*, recorded via testlog — and this
test opens none of the files it audits. So cmd/go correctly concluded nothing
had changed, and replayed a stale PASS. Run with `-count=1` the same violation
produced four detailed failures, so the logic was right the whole time; only its
cache key was wrong.

**Fixed by:** making the cache key correct rather than defeating the cache. The
test now reads every `.go` file in the module before asserting anything, so any
source change invalidates it. Verified end to end: warm the cache to a PASS,
plant the violation, run with no flags — four violations, `FAIL`.

**Worth saying out loud: yes, and it belongs next to #5.** It is the same lesson
arriving from a third direction, and by now that is the pattern rather than the
anecdote:

- In #4 the test measured at a moment when the property was unobservable.
- In #5 I trusted the instrument and blamed the code.
- Here the test was correct, the code was correctly broken, and the *reporting
  layer between them* served a stale answer.

Three different components in the chain from "is it true?" to "does it say so?",
each of which failed once. A green check mark is a claim made by a whole
pipeline, not by the assertion at the end of it. The thing worth asking of any
guard you rely on is not "does it pass?" but "have I ever seen it fail, on
purpose, in the exact way I would invoke it?" — and for this one the honest
answer was no until I checked.

---

## 11. Loopback lies about backpressure

*2026-08-23*

**Broke:** The test asserting that a slow client costs itself frames reported
`backpressure drops = 0`, while nine healthy clients each received 776 frames.
The drop policy — the single mechanism act two rests on — appeared not to work.

**Why:** It worked fine. The test could not see it. Linux auto-tunes loopback
socket buffers into the megabytes, so a client that never calls `ReadMessage`
still absorbs several hundred kilobytes before the server's writes block. Until
the write pump blocks, the send channel never fills, and the drop counter is
correctly zero. A phone on venue wifi has no such buffer.

**Fixed by:** dialling that one client through a `net.Dialer` whose `Control`
sets `SO_RCVBUF` to the kernel minimum, which reproduces a constrained consumer
in about a second. The counter went from 0 to 557 while the healthy clients
still received 1,780 frames each — one slow consumer costing itself and nothing
else, which is the property, demonstrated rather than asserted.

**Worth saying out loud:** yes, briefly, and it is a good pairing with the
chaos-layer argument. Testing a distributed failure on one machine means the
machine's own generosity is part of your test fixture. The environment has
defaults that are kinder than production, and a green test can mean "the
condition never occurred" rather than "the code handled it."

---

## 12. The end-to-end test measured the lag after the lag had drained

*2026-08-23*

**Broke:** The first full two-tab run armed chaos at a 1.2 second delay, tapped
some orders, waited 2.2 seconds, and read `behind by 0.0s / 0 frames` with zero
diverged levels. It looked like the chaos line was not delaying anything.

**Why:** The delay was 1.2s and the measurement was at 2.2s. Every delayed frame
had already been delivered. Lag is only observable while frames are still in
flight; after the traffic stops, a correctly-working delay line is
indistinguishable from one that does nothing.

**Fixed by:** driving sustained load with `cmd/swarm` and sampling the projector
repeatedly *during* it, keeping the worst divergence seen. It then read `behind
by 1.5s / 72 frames`, ten diverged levels, 162 frames never delivered, and all
four invariants still green.

**Worth saying out loud: yes — this is the third one.** Entry 4 was the freeze
test measuring at the end of the tape. Entry 10 was the architecture test
serving a cached answer. This is the same shape again: *the code was right, and
the measurement was taken where the property could not be seen.*

Three instances in one build is not bad luck, it is a category. The useful
formulation to give the room: **before trusting a green result, ask what the red
version would have looked like and whether your setup could have produced it.**
For all three of these the honest answer was no.

---

## 13. The room grid worked against the fixture and was empty against the server

*2026-08-23*

**Broke:** The presenter panel — one pulsing cell per trading session, the thing
that separates "the room stopped trading" from "delivery broke" — rendered
perfectly against the fixture tape and was completely empty against the live
server.

**Why:** The tape carries a `roster` array, because when it was hand-authored a
roster was the obvious way to describe who was in the room. The view read it. The
real server has no roster and cannot have one: nobody logs in, sessions appear
when a phone connects, and the server never knows who is present until they act.
So the view depended on a field only the fixture ever supplied.

**Fixed by:** deriving the grid from the sessions actually observed acting, which
the book frames already carry as `actor`. The roster is now an optional
supplement rather than the source. Under real load: ten cells, all pulsing, ids
drawn from the connected clients.

**Worth saying out loud: yes, and it is the strongest self-criticism available,
because it cuts against this project's own headline decision.** Building the view
first against a hand-authored fixture is the thing I would defend hardest about
the build order: it made the wire format concrete, it made the view provably
independent of the engine, and it produced a working screen before a line of Go
existed.

And it has exactly this failure mode. A fixture is written by the same person who
writes the view, at the same moment, so it supplies whatever the view finds
convenient. That is precisely what makes it productive, and precisely what makes
it able to encode an assumption the real system can never satisfy. The golden
test in `internal/wire` catches the reverse direction — a field the view reads
that the server does not send — but it cannot catch this one, because the field
*was* sent, by the fixture, forever.

The honest conclusion is not "do not build view-first". It is that fixture-first
buys you a finished view and owes you one integration pass whose only job is to
find what the fixture was quietly providing. Budget for that pass; do not
discover it on stage.

---

## 14. I nearly shipped a rehearsal flag that does nothing, on a confounded measurement

*2026-08-31*

**Broke:** Asked to add a way to make the projector's slow-consumer drop counter
move during a local rehearsal — it reads 0 on localhost because the kernel
auto-tunes socket buffers — I added a `-rcvbuf` flag to `cmd/swarm` that shrinks
a blackholed client's `SO_RCVBUF` to the kernel minimum. It mirrored what the
server test already did, so I was confident in it.

It did not work. Then it appeared to work. Both readings were wrong.

**Why the first reading was wrong:** the flag genuinely did nothing at the rate I
first tried, because the blackholed clients subscribe as *traders*, and the
trader subscription is the lowest-volume feed in the system. The server's own
send buffer absorbed everything before the client's receive buffer mattered.

**Why the second reading was worse:** I then ran three conditions in sequence —
no flag, flag, flag with a different feed — against **one long-lived server
process**, and read 0, then 1346, then 3853, then 6390. It looked like a clean
dose-response curve. It was one cumulative counter. The projector's drop counter
counts for the life of the server, so every run inherited the previous run's
total. I had built a measurement in which *any* change appears to help, because
the number only ever goes up.

The tell was there and I nearly walked past it: the **control** read higher than
the treatment. That cannot happen if the treatment is the cause, and it is
exactly what you would expect if elapsed traffic is the cause.

**What the clean experiment said,** one fresh server process per condition:

| condition | `dropped · slow phone` |
|---|---|
| rate 6, no `-rcvbuf` | 0 |
| rate 6, with `-rcvbuf 2048` | 0 |
| rate 30, no `-rcvbuf` | 1352 |
| rate 30, with `-rcvbuf 2048` | 1312 |

and separately, at rate 30 with no flag at all:

| condition | `dropped · slow phone` |
|---|---|
| blackhole on the projector feed | 1337 |
| blackhole on the trader feed | 0 |

**Throughput and feed choice are the variables. The socket buffer is not.**
1352 versus 1312 is noise in the opposite direction from the hypothesis.

**Fixed by:** deleting the flag. It was already written, already documented, and
already had a help string explaining its purpose — and it demonstrably did
nothing. Shipping it would have added a knob that a future reader would trust,
reach for, and be misled by. What survives is the change that was measured to
matter (blackholed clients subscribe to the busiest feed) and an accurate note
in `swarm` telling you to raise the rate instead.

**Worth saying out loud: yes — this is the fourth one, and the first where I
built the broken instrument myself.**

Entry 5 was an instrument that was wrong. Entry 10 was an instrument that was
dead. Entry 13 was an instrument more generous than production. This one is
different in kind: the earlier three were inherited or accidental, and this was a
measurement apparatus I designed, in a project whose entire thesis is that you
should verify your instruments.

The specific failure — **reading a cumulative counter across successive trials
and calling the differences an effect** — is not exotic. It is the most ordinary
benchmarking mistake there is, and I made it while writing the document that
warns other people about measurement traps.

The generalisation, and the one I would give the room: **a measurement where the
number can only go up is not a measurement.** Ask what the control looks like
before you run the treatment. If you cannot describe a result that would make you
abandon the change, you are not testing it.
