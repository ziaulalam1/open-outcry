# Shot list — open-outcry workshop

For whoever is holding the camera. You do not need to understand the demo. You
need three photographs, and one of them always works.

**The single most important instruction: do not turn the house lights off.** A
dark room with a bright screen turns the presenter into a black silhouette, and
every photograph becomes unusable. Dim is fine. Dark is not. If someone reaches
for the light switch, stop them.

Second most important: **stand beside the screen, never in front of it**, and
never let the presenter walk between you and it.

---

## The verbal cue

The presenter will say, clearly and out of nowhere:

> ### "Let's break it."

That means **the split screen is about thirty seconds away.** Get into position
for Shot 1 and start shooting when the screen divides in two. Keep shooting.
The interesting frames are in the twenty seconds after the split appears, while
the right-hand side is visibly falling behind.

There is no second cue. If you miss it, Shot 3 still works and can be taken at
any point during act two.

---

## Shot 1 — Podium, mid-gesture, split legible behind

**The photograph this whole event exists to produce.**

- **Where you stand:** three-quarters on to the presenter, angled so the screen
  is over their shoulder and readable — not square-on to the screen, not square
  on to the presenter.
- **Frame:** presenter from roughly the waist up, occupying the left or right
  third. The split screen fills most of the remaining frame. A few audience
  heads in the near foreground, dark and out of focus — they establish that
  this is a room with people in it rather than a rehearsal.
- **Timing:** mid-gesture. Hands moving, mouth open, pointing at the screen.
  A still, symmetrical, hands-at-sides frame reads as a posed photograph and
  defeats the purpose.
- **Must be legible in the frame:** the two panes side by side, and if possible
  the amber banner across the top. Do not worry about reading the numbers.

**Burst mode. Hold it down. Twenty frames of a gesture gives one good one.**

---

## Shot 2 — Wide room, heads on phones

**The proof that the room was participating, not watching.**

- **Where you stand:** back corner of the room, or the side aisle at the back.
- **Frame:** as much of the audience as you can get, with the screen visible and
  lit at the top or side of the frame. Faces are not the subject; the subject is
  **a lot of people looking down at phones while a screen shows a live market.**
- **Timing:** early in act one, when people have just scanned in and are
  submitting their first orders. That is when the most hands are up.
- Nobody needs to be identifiable. Backs of heads are ideal.

---

## Shot 3 — Screen only, at the split

**The fallback that always works.** No people, no lighting problem, no timing
risk. If everything else fails, this one still carries the story, and it can be
taken during rehearsal with nobody in the room.

- **Where you stand:** directly in front of the screen, far enough back that the
  frame is filled edge to edge with screen and nothing else.
- **Frame:** square on, screen fills the frame, no ceiling, no floor, no podium.
- **Timing:** during act two while the right-hand pane is behind. Best moment is
  when several price rows are outlined in amber with signed numbers beside them.
- **The presenter can freeze the frame for you.** Ask. Pressing the spacebar
  holds the picture on screen for as long as you need while the system keeps
  running underneath. Take your time; there is no hurry once it is frozen.

---

## Camera settings

The hard problem is dynamic range: a very bright screen in a dim room. The
instinct is to expose for the room, which blows the screen into a white
rectangle and destroys the only thing worth photographing.

**Expose for the screen. Let the room go dark. That is the correct exposure.**

### On a phone

1. Tap the screen area to focus.
2. **Drag the exposure slider DOWN**, usually one to two stops, until the
   screen's text is crisp rather than glowing.
3. Lock focus and exposure (press and hold until AE/AF LOCK appears), so the
   camera stops re-metering every time the presenter moves.
4. **Flash off. Night mode off. HDR off.** Night mode blurs gesture; HDR lifts
   the audience and flattens the screen contrast, which is the opposite of what
   Shot 1 needs.
5. **Burst mode** for Shot 1 — hold the shutter.
6. Landscape orientation for all three.

### On a camera

| | Shot 1 (podium) | Shot 2 (room) | Shot 3 (screen only) |
|---|---|---|---|
| Mode | Manual | Manual | Manual, tripod if possible |
| Aperture | f/2.0–f/2.8 | f/2.8–f/4 | f/5.6 (edge sharpness) |
| Shutter | **1/125 minimum** | 1/80 | 1/60 or slower |
| ISO | 1600–3200 | 1600–3200 | 400–800 |
| Exposure comp. | −1 to −1.7 EV | −1 EV | meter on the screen |
| White balance | Tungsten / ~3200K | same | same |
| Drive | Burst | Single | Single |

**1/125 is a floor for Shot 1, not a preference.** Slower and the moving hand
smears, which is exactly the frame you were trying to get.

### If you see dark bands rolling across the screen

That is the projector's refresh cycle beating against your shutter. Fix, in
order: drop to 1/60; then 1/50; then 1/30 on a tripod for Shot 3. For Shot 1 you
cannot go below 1/125 without losing the gesture, so if banding persists,
prioritise Shot 3 for a clean screen and accept some banding on Shot 1.

---

## What the screen looks like when it is worth shooting

You are looking for **two panes side by side that disagree**:

- Left pane, green heading: **LIVE FEED**. Right pane, amber heading:
  **DEGRADED FEED**.
- Amber-outlined rows in the right pane with signed numbers next to them
  (`-1,250`, `+750`). Those are the prices that have drifted apart. **The more
  amber rows, the better the photograph.**
- Top of the right pane: `behind by 1.5s / 72 frames`.
- An amber banner across the top in plain English.
- Along the bottom, four green ticks: `CONSERVATION`, `NO CROSSED BOOK`,
  `PRICE-TIME PRIORITY`, `SWEEP COMPLETE`. **These staying green while the right
  pane rots is the entire point of the photograph.** If they are green and the
  right pane is amber, shoot it.

Ask the presenter for **hero mode** before Shot 3 — it makes all the type about
a third larger, which matters when the photo is viewed as a thumbnail.

---

## Rehearsal — getting a Shot 3 today, with no audience

Run this on the presenting laptop. It produces the exact screen described above
with no room, no phones and no audience, so the fallback shot can be banked
before the day. Four terminals, or three plus a browser.

**1. Start the server.**

```
go run ./cmd/open-outcry -port 8080
```

It prints the LAN address. For a rehearsal you can ignore that and use
`localhost`.

**2. Open the projector, full screen.**

```
http://localhost:8080/?view=projector
```

Press `F11` (or `⌃⌘F`) for full screen. You should see a seeded book — it is
never empty, by design, so there is always something to photograph.

**3. Fill the room with simulated traders.**

```
go run ./cmd/swarm -addr localhost:8080 -n 24 -rate 30 -blackhole 2 -dur 5m
```

**`-rate 30` is not arbitrary and should not be lowered.** See the drop-counter
note below.

**4. Break it.** In another terminal, or a browser tab:

```
curl "http://localhost:8080/chaos?armed=1&delay=1400&drop=3"
```

The screen splits within a second or two. Give it **fifteen to twenty seconds**
before shooting — the divergence builds as the right-hand pane falls further
behind, and the best frames are not the first ones.

**5. Make it photogenic.**

- Press `h` on the projector window for **hero mode**: +30% type, non-essential
  chrome hidden. Do this before shooting.
- Press `space` to **freeze the frame**. The picture holds while the system keeps
  running underneath, so you can take as long as you like lining up the shot.
  Press `space` again to release.
- If the divergence looks thin, wait. If it looks good, freeze immediately —
  it will not get better by waiting once several rows are amber.

**6. Put it back.**

```
curl "http://localhost:8080/chaos?armed=0"
```

The right-hand pane catches up within a couple of seconds and the split closes.
Worth watching once so the recovery is not a surprise on the day.

---

## ⚠ The drop counter reads zero on a laptop. It is not broken.

The projector shows two separate drop counters along the bottom:

- **`dropped · chaos`** — frames thrown away deliberately. This moves as soon as
  chaos is armed, on any machine, always.
- **`dropped · slow phone`** — frames lost because a client could not keep up.
  **On localhost this often reads 0, and that is expected.**

The kernel auto-tunes loopback socket buffers into the megabytes, so a client
that has stopped reading still absorbs an enormous amount before anything backs
up. A phone on venue wifi has no such buffer, and the counter moves on its own.

**Measured on this machine, one fresh server per run, 12 clients, 2 blackholed,
45 seconds:**

| condition | `dropped · slow phone` |
|---|---|
| `-rate 6` | **0** |
| `-rate 30` | **~1,340** |

Throughput is the variable. An earlier attempt to force the counter by shrinking
the client's socket receive buffer was measured and **removed** — at the same
rate it made no difference (1352 without, 1312 with). See `docs/build-log.md`
entry 14.

**So: if you want to watch that counter climb during rehearsal, use
`-rate 30` and let it run for at least 45 seconds.** Below about `-rate 20`,
swarm will print a reminder telling you the same thing.

**Do not debug this on the day.** If the counter reads 0 in the room, it means
nobody's phone has fallen behind yet, which is good news, not a fault.

---

## The one-paragraph version, for a phone in your hand

House lights stay ON. Stand beside the screen, never in front. When the
presenter says **"let's break it"**, you have thirty seconds — get the podium,
the gesture, and the divided screen in one frame, expose *down* so the screen is
readable rather than glowing, and hold the shutter. If any of that goes wrong,
walk up to the screen during act two, ask for hero mode and a freeze, and take
Shot 3 square on. Shot 3 always works.
