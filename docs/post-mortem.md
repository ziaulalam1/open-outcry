# Post-mortem — STRUCTURE ONLY

**Status: stub. Nothing here is filled in, because the talk has not happened.**

This file exists now so that it is not invented later. The highest-signal
content it will hold — the questions that beat me — is only capturable in the
hour after the talk, and it will be gone by the next day.

## Fill this in the same day. Not the next morning.

The specific decay: within a day the questions get smoothed into questions I
*could* answer, and the interesting ones vanish. Write ugly, write fast, fix the
prose later.

---

## 1. What happened

- Venue, date, headcount, how many actually connected phones
- Which version was delivered (45 min / 25 min / other)
- What ran: live demo, fixture fallback, or a mix — and why

---

## 2. Questions I could not answer

**The reason this file exists. Verbatim, attributed if the person is willing.**

Do not paraphrase into something answerable. If it stung, that is the signal.

| # | Question, verbatim | Who asked | What I said in the room | What the real answer is |
|---|---|---|---|---|
| | | | | |

For each one, afterwards:

- Is this a gap in the build, or a gap in my understanding? They are different
  problems.
- Does it change the code, the talk, or neither?
- If it changes the code: does it become a build-log entry?

---

## 3. Questions I answered badly

Separate from §2 on purpose. Answering fluently and wrongly is worse than
saying "I don't know", and it is the failure mode I will not notice at the time.

| Question | What I said | What was wrong with it |
|---|---|---|
| | | |

---

## 4. What broke

Demo failures, room failures, wifi, QR, projector, timing. Everything, including
things nobody noticed.

- Did the drop counter move on venue wifi without intervention?
  **This is the one open empirical question from [RUNBOOK.md](RUNBOOK.md) §9 —
  it has never been measured on real phones. Record the number.**
- Did the QR scan reliably, at what distance, on which phones?
- How many phones connected out of how many people?
- Did anything in the room produce a behaviour `cmd/swarm` does not simulate?

---

## 5. Where the room actually engaged

Which beat landed. Where attention was lost. Whether act 2.5's question produced
an argument or silence — and if silence, whether the question was badly framed
or the room was wrong for it.

Act three's three stories: which one landed hardest, and was it the one expected?

---

## 6. What I would cut

Written before re-reading the slides, so the cut is based on the room rather
than on the plan.

---

## 7. Changes this produces

| Change | To what | Priority |
|---|---|---|
| | | |

---

## 8. Artifacts captured

- [ ] Photo — podium mid-gesture, split legible behind
- [ ] Photo — wide room, heads on phones
- [ ] Photo — screen only at the split
- [ ] Recording, and whether the audio caught the questions
- [ ] Anything an attendee posted

See the shot list (kept with the workshop material, outside this repository).
