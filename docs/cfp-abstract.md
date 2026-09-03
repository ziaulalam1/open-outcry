# CFP abstract — STUB

**Status: stub.** Blocked on one decision only: **which venue.** Everything else
below is draftable now and is drafted; the venue determines duration, audience
level, and the bio, so it is left open rather than guessed.

---

## Blocked on

| Field | Needs |
|---|---|
| **Venue** | Not picked. |
| **Duration** | Follows the venue's slot. The deck covers 45 min and 25 min; anything else needs a re-cut. |
| **Date** | Follows the venue's CFP deadline. |
| **Audience level** | Follows the venue. Drafts below assume working engineers, not students. |

---

## Title — candidates, none chosen

1. **Three Ways a Measuring Instrument Lies** — leads with act three. Strongest
   for a testing/quality track. Undersells the live demo.
2. **Correct While Broken: A Live Order Book, Degraded on Purpose** — leads with
   act two. Strongest for a distributed-systems track.
3. **Open Outcry: Thirty Phones, One Order Book, No Locks** — leads with the
   demo. Strongest for a Go track.

Pick by track, not by preference.

---

## Abstract — draft, ~180 words

> Everyone in the room opens a URL and starts trading against each other. A
> matching engine on a laptop builds an order book live on the projector, and
> price-time priority stops being a definition the moment you watch your own
> order sit behind someone else's at the same price.
>
> Then I break it on purpose. Delay is injected and a third of the frames are
> dropped on one feed, the screen splits into a live book and a degraded one,
> and the degraded side falls seconds behind while a continuously-running
> invariant checker stays green throughout. Both books are correct; only
> delivery degraded. That is the question I want to argue about with the room:
> your users are about to trade on prices that no longer exist — do you show
> them stale data, or nothing?
>
> The last third is about measurement. Three times while building this, the
> instrument was the thing that was wrong: a decoder with a bad lookup table, an
> architecture test that printed PASS while dead, and a fixture more generous
> than production could ever be. All three found by accident, in one project.

**Not yet checked against any venue's word limit.**

---

## Audience — draft

Working backend and distributed-systems engineers. No trading knowledge assumed
— the market-structure content is taught live in the first ten minutes and the
room demonstrates it to itself. Familiarity with concurrency primitives helps
for act one but is not required for acts two and three.

**Takeaways:**

1. Why correctness and delivery are separate properties, and why conflating them
   is most of what is hard about market data.
2. Single-owner concurrency as a structural alternative to lock discipline —
   what it buys, and what it costs at volume.
3. A method for auditing your own guards: have you watched this instrument fail
   on purpose, in the exact way you invoke it?

---

## Still to write when the venue exists

- [ ] Bio, to the venue's word limit
- [ ] A/V requirements — **projector plus attendee wifi on the same LAN is a
      hard requirement for acts one and two.** Fixture mode is the fallback and
      must be declared if the venue cannot promise it.
- [ ] Whether recording is permitted and who owns it — the recording is the
      artifact this whole effort is for
- [ ] Prior-speaking field — currently empty, and this would be the first
- [ ] Attendee count expectations: the demo needs roughly 10+ phones to produce
      a book with visible depth. Below that, `cmd/swarm` fills the room, and
      whether that is acceptable to disclose on stage is a decision to make
      before submitting, not during
