# Checkpoint 05 — publish, and photo readiness

**2026-08-31**

First checkpoint written as a repo artifact. Checkpoints 01–04 (structure
proposal, projector-from-fixtures, engine + invariant + tests, transport +
gates) were reported conversationally at the time; their evidence lives in the
commits and in `docs/build-log.md`. From here they are files.

---

## 1. The repository is public

**https://github.com/ziaulalam1/open-outcry**

| | |
|---|---|
| Visibility | public |
| Default branch | `main` (renamed from `master` before first push) |
| Commits | **7**, unsquashed, in build order |
| Module path | `github.com/ziaulalam1/open-outcry` — matches the repo path, which the import paths depend on |
| Attribution | all seven commits linked to `ziaulalam1` |

### Two things decided before the first push, because neither is reversible after

**Author email.** All seven commits were authored as
`Ziaul Alam <undisputedfx@gmail.com>`. Pushing would have put a personal address
into permanent, scrapeable public metadata. Rewritten to GitHub's ID-qualified
noreply form (`206639074+ziaulalam1@users.noreply.github.com`) before the remote
existed.

The rewrite preserved everything that matters: seven commits, same messages,
same order, same authored dates, nothing squashed. Verified by comparing every
commit's tree hash against a pre-rewrite backup — **7 matching, 0 mismatched** —
so only commit metadata changed, not a byte of content.

The jobs address was considered and rejected: a portfolio repo whose commit log
reads as a contact channel looks manufactured for applications, which undercuts
what the history is for.

**Branch name.** `master` → `main`, matching the GitHub default so the repo's
default branch and any clone instructions agree.

### Pre-push audit

- 48 tracked files, 556K.
- Secret scan over the working tree **and every blob in history**: clean. The
  only hit was the phrase "a signed token" in prose.
- Leak scan for host paths, the parent repo, `canon/`, named contacts, and
  positioning language: **zero hits**. `canon/workshop-canon.md` contains
  positioning and named contacts and correctly lives only in the private parent.
- Parent repo interference: none. `/project/.gitignore:49` ignores `projects/`,
  the parent tracks nothing beneath it, and the nested repo is invisible to it.

### Correction to canon

`canon/workshop-canon.md` §2 said "Six unsquashed commits". There are seven.
Corrected, along with the artifacts table and the open-items list.

---

## 2. `docs/shot-list.md`

Written for someone holding a camera who does not know what the demo does.

- **Three named shots**, in decreasing order of risk: podium mid-gesture with the
  split legible behind and audience heads dark in the foreground; wide room with
  heads on phones; screen-only at the split, which always works and needs no
  audience.
- **The verbal cue** — the presenter says *"Let's break it"*, which means the
  split is about thirty seconds out. There is no second cue.
- **House lights stay on.** Stated first and repeated, because a dark room turns
  the presenter into a silhouette and makes every frame of Shot 1 unusable.
- **Camera settings** expose for the screen and let the room fall dark, with a
  table for phone and for camera, and the fix for projector refresh banding
  (drop shutter, prioritise the screen-only shot).
- **A rehearsal recipe** that produces the act-two screen with no audience, so
  the fallback shot can be banked in advance.

---

## 3. The drop-counter trap, measured rather than described

Documented prominently in the shot list, and the reason a flag was added and then
deleted in the same session.

The projector's `dropped · slow phone` counter reads 0 on a laptop. The kernel
auto-tunes loopback socket buffers into the megabytes, so a client that has
stopped reading absorbs an enormous amount before anything backs up.

The requested fix was a flag shrinking `SO_RCVBUF` for local runs. It was built,
measured, and **removed, because it does nothing.** One fresh server process per
condition:

| condition | `dropped · slow phone` |
|---|---|
| rate 6, no `-rcvbuf` | 0 |
| rate 6, with `-rcvbuf 2048` | 0 |
| rate 30, no `-rcvbuf` | 1352 |
| rate 30, with `-rcvbuf 2048` | 1312 |

and, at rate 30 with no flag:

| condition | `dropped · slow phone` |
|---|---|
| blackholed client on the projector feed | 1337 |
| blackholed client on the trader feed | 0 |

**Throughput and feed choice are the variables; the socket buffer is not.**

What shipped instead: blackholed swarm clients subscribe to the projector feed
(the measured difference between 1337 and 0), and `swarm` prints an accurate
note telling you to raise the rate. The rehearsal recipe is `-rate 30 -dur 45s`.

The first version of this measurement was confounded — three conditions run
against one long-lived server, reading a cumulative counter, producing a
convincing dose-response curve out of nothing. `docs/build-log.md` entry 14.

---

## 4. State of the gates

Unchanged from checkpoint 04 and re-run on the published tree:

```
gofmt -l .        empty
go vet ./...      clean
go test ./...     11 packages, 79 tests, green
```

---

## 5. Next, and one open decision

Priority order from here: **Kafka ingest → OTel/Prometheus/Grafana → README and
slides.**

**Blocker to surface before writing the Kafka path:** this container has no
Docker (not installed, and the socket is off-limits by standing rule) and no
broker binary. Go clients are reachable — `franz-go v1.21.6`,
`segmentio/kafka-go v0.4.51` — and Redpanda's release assets are reachable, so a
broker could be downloaded and run directly, but that is a ~200MB single-purpose
dependency in a container that is otherwise clean.

This matters because the deliverable is *replay*, not throughput. The property
that has to be proven — a recorded command log replays to bit-identical book
state — is a property of the engine and the log, not of the broker, and is fully
testable without one. The broker is how the log is transported, not what makes
the claim true.

Recorded here rather than decided unilaterally, because "the résumé bullet names
Kafka" makes the difference between *wired and compiling* and *exercised against
a live broker* a distinction worth being explicit about.
