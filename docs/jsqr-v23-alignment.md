# jsQR misreads every version 23 QR code at error-correction level L

**Component:** [jsQR](https://github.com/cozmo/jsQR) 1.4.0 (latest, published 2021-04-24)
**Severity:** silent decode failure, confined to one QR version at one ECC level
**Status:** unreported upstream — the project is dormant (evidence in §7)
**Written:** 2026-08-23

---

## 1. Summary

jsQR's alignment-pattern table contains one wrong coordinate. For QR **version 23**
it lists

```
[6, 30, 54, 74, 102]
```

where ISO/IEC 18004 requires

```
[6, 30, 54, 78, 102]
```

`74` is version **22's** fourth alignment coordinate, duplicated one row down — an
adjacent-row transcription error. jsQR therefore samples version 23 symbols on a
grid that is misaligned in one band. At ECC levels M, Q and H the extra
redundancy absorbs the resulting bit errors and the symbol still decodes. At
level L it does not, and jsQR reports *no QR code found at all* rather than a
decode error.

Every other version, 1 through 40, is correct.

**Who is affected:** anyone decoding a version 23 (109×109 module) QR code at ECC
level L with jsQR. That is roughly 700–1100 bytes of payload — large for a URL,
ordinary for an embedded JSON blob, a signed token, a vCard, or an offline data
capsule. The failure is silent and total: the scanner simply never sees a code,
which reads to a user as "bad lighting" or "hold it steadier".

---

## 2. Why the table is derivable, not a matter of opinion

This is the part that makes the bug provable rather than a disagreement between
two tables. ISO/IEC 18004 Annex E does not publish alignment centres as free-form
data; it **constructs** them. For version `v`:

```
size   = 4v + 17                                  # modules per side
n      = floor(v / 7) + 2                         # number of alignment tracks
first  = 6                                        # pinned
last   = size - 7
step   = ceil((size - 13) / (2 * (n - 1))) * 2    # rounded UP to an even number
tracks = [6] + [last - step*i for i in n-2 .. 0]  # built DOWNWARD from `last`
```

Two details matter and are the ones a from-memory reconstruction gets wrong:

1. **The step is forced even.**
2. **The sequence is built downward from the last track**, with the first pinned
   at 6 — so any remainder is absorbed by the **first** gap and never
   distributed through the interior.

Consequence, and this is the tell: **the interior gaps are always uniform.** Only
the first gap may differ. A row whose interior gaps vary cannot be produced by
this construction at all.

### Version 23

```
size = 4(23) + 17 = 109
n    = floor(23/7) + 2 = 3 + 2 = 5
last = 109 - 7 = 102
step = ceil((109 - 13) / (2 * 4)) * 2 = ceil(96/8) * 2 = 12 * 2 = 24

downward from 102:  102, 78, 54, 30      plus the pinned 6
                 => [6, 30, 54, 78, 102]      gaps 24, 24, 24, 24
```

### Version 22 — the source of the bad value

```
size = 4(22) + 17 = 105
n    = 5
last = 105 - 7 = 98
step = ceil((105 - 13) / 8) * 2 = ceil(11.5) * 2 = 12 * 2 = 24

downward from 98:  98, 74, 50, 26        plus the pinned 6
                => [6, 26, 50, 74, 98]       gaps 20, 24, 24, 24
```

`74` belongs to version 22. jsQR's version 23 row —

```
[6, 30, 54, 74, 102]     gaps 24, 24, 20, 28
```

— has a **non-uniform interior** (`24, 24, 20, 28`), which the construction can
never generate, because the interior tracks come from repeated subtraction of a
single constant. The defect is visible from the shape of the row alone, without
consulting any reference table.

> **Do not use the shortcut `(last - first) / (n - 1)`.** For version 23 it gives
> 24 and appears to work. For version 22 it gives 23 — an odd number, where the
> true step is 24. The rounding and the downward construction are load-bearing.

---

## 3. The observed table

From the published bundle, `node_modules/jsqr/dist/jsQR.js`:

```
$ grep -oE "\[6, ?30, ?54, ?[0-9]+, ?102\]" node_modules/jsqr/dist/jsQR.js
[6, 30, 54, 74, 102]
```

For comparison, `python-qrcode` 8.2, an unrelated implementation:

```python
>>> from qrcode.util import pattern_position
>>> pattern_position(22)
[6, 26, 50, 74, 98]
>>> pattern_position(23)
[6, 30, 54, 78, 102]
>>> pattern_position(24)
[6, 28, 54, 80, 106]
```

---

## 4. Reproduction

Uses **only third-party code**: `python-qrcode` to encode, `jsQR` to decode.
Nothing here depends on the project that found the bug.

```
pip install qrcode
npm install jsqr
```

`encode.py` — writes the module matrix as `0`/`1` rows, so no image library is
needed:

```python
import sys, qrcode
from qrcode.constants import ERROR_CORRECT_L, ERROR_CORRECT_M, ERROR_CORRECT_Q, ERROR_CORRECT_H
ECC = {"L": ERROR_CORRECT_L, "M": ERROR_CORRECT_M, "Q": ERROR_CORRECT_Q, "H": ERROR_CORRECT_H}
version, ecc, payload, out = int(sys.argv[1]), sys.argv[2], sys.argv[3], sys.argv[4]
q = qrcode.QRCode(version=version, error_correction=ECC[ecc], box_size=1, border=0)
q.add_data(payload); q.make(fit=False)
assert q.version == version
with open(out, "w") as f:
    for row in q.get_matrix():
        f.write("".join("1" if c else "0" for c in row) + "\n")
```

`decode.mjs` — rasterises the matrix and hands it to jsQR:

```js
import { readFileSync } from 'node:fs'
import { createRequire } from 'node:module'
const jsQR = createRequire(import.meta.url)('jsqr')

const [file, expected] = process.argv.slice(2)
const rows = readFileSync(file, 'utf8').trim().split('\n')
const size = rows.length, SCALE = 6, QUIET = 4
const dim = (size + QUIET * 2) * SCALE
const px = new Uint8ClampedArray(dim * dim * 4).fill(255)
for (let y = 0; y < size; y++) for (let x = 0; x < size; x++) {
  if (rows[y][x] !== '1') continue
  for (let dy = 0; dy < SCALE; dy++) for (let dx = 0; dx < SCALE; dx++) {
    const i = (((y + QUIET) * SCALE + dy) * dim + ((x + QUIET) * SCALE + dx)) * 4
    px[i] = px[i + 1] = px[i + 2] = 0
  }
}
const r = jsQR(px, dim, dim)
console.log(`${file}  ${size}x${size}  ${r && r.data === expected ? 'DECODED' : (r ? 'WRONG DATA' : 'NO QR CODE FOUND')}`)
```

### Observed output

One 297-byte payload, which fits version 23 at **every** ECC level — so data
density is held constant and only the ECC level varies:

```
=== version 23, all four ECC levels, ONE payload ===
  a23L.txt   109x109  NO QR CODE FOUND      <-- the bug
  a23M.txt   109x109  DECODED
  a23Q.txt   109x109  DECODED
  a23H.txt   109x109  DECODED

=== neighbouring versions at ECC L, same payload ===
  b21L.txt   101x101  DECODED
  b22L.txt   105x105  DECODED
  b24L.txt   113x113  DECODED
  b25L.txt   117x117  DECODED
```

Because the payload is identical across the first four rows, this rules out
payload length, data density, and encoder capacity tables as explanations. The
only variable is how much redundancy is available to absorb the mis-sampling.

---

## 5. The decisive test

Patch a **copy** of the published bundle, changing one character:

```
[6, 30, 54, 74, 102]   ->   [6, 30, 54, 78, 102]
```

```
=== the decisive one-character test ===
  patched 1 occurrence of jsQR's v23 row: 74 -> 78
  a23L.txt   with v23 row corrected: DECODED
```

One character, and every version 23 ECC L case that previously reported "no QR
code found" decodes byte-exactly. Nothing else is affected.

---

## 6. Why only ECC level L

Alignment patterns tell the decoder where the module grid actually is across the
symbol, correcting for perspective and lens distortion. A misplaced alignment
centre means the decoder samples a band of the symbol at slightly wrong
coordinates, reading some modules from the wrong side of a boundary.

That injects a roughly fixed number of bit errors, independent of ECC level.
Reed–Solomon then either absorbs them or does not:

| ECC | approx. correction capacity | result at v23 |
|-----|-----------------------------|---------------|
| L   | ~7 %                        | **fails**     |
| M   | ~15 %                       | decodes       |
| Q   | ~25 %                       | decodes       |
| H   | ~30 %                       | decodes       |

This ECC-dependence is itself diagnostic. A wrong **capacity** or **block
structure** table cannot produce an ECC-dependent failure of this shape — it
would corrupt the data layout at every level equally. A wrong **sampling grid**
can, and does. If you are chasing something similar: failure that varies with
redundancy points at geometry, not at the data tables.

---

## 7. Upstream status: dormant

Checked 2026-08-23 against the npm registry and GitHub APIs.

| Signal | Value |
|---|---|
| Latest release | **1.4.0, 2021-04-24** |
| Last commit on `master` | **2021-08-24** |
| Open pull requests | 18 — oldest 2021-06, newest 2024-08, **none merged** |
| Open issues | 97 |
| Maintainer comments among the 30 most recent issue comments (spanning 2022-12 → 2025-07) | **0** |
| Repository archived? | **No** |
| Stars | ~4,000 |

The repository is not archived, and that is what makes this worth writing down
rather than shrugging at. Nothing on the project page signals that it is
unmaintained, so it continues to be adopted, and the defect stays
discoverable-but-unfixed. (The npm `modified` timestamp is recent for registry
housekeeping reasons and says nothing about the code.)

An issue filed against a repository with 97 open issues and no maintainer replies
in three years is not a fix; it is a message in a bottle. Hence a public writeup
instead — indexable by whoever hits this next, and self-contained enough to be
verified without trusting me.

---

## 8. If you are affected

- **Use ECC M or higher** if your payload lands on version 23. This is a
  reasonable default anyway for anything scanned off a screen or in poor light.
- **Or avoid version 23** by nudging payload length across the boundary in either
  direction — versions 22 and 24 are unaffected.
- **Or patch the table.** The change is one character; §5 shows it is sufficient
  and self-contained.
- **Do not "fix" your encoder to match.** Producing symbols with jsQR's incorrect
  coordinate would make them decodable by jsQR and misreadable by every
  spec-conformant scanner, including ZXing and phone cameras. The encoder is not
  what is wrong here.

---

## 9. How this was found

A hand-written QR encoder was being verified by round-tripping through jsQR as an
independent oracle, across all 160 (version, ECC) combinations. 223 of 224 cases
passed. The single failure was investigated as an encoder bug — isolated to one
cell, neighbours clean, which is a strong signal for a wrong lookup-table entry —
and a fix was drafted for the encoder.

That conclusion was wrong. The encoder was correct; the oracle was not.

The general form is worth stating: *when a measurement disagrees with the thing
being measured, there are two suspects, and it is easy to charge only one.* The
tell here was available early and walked past — the failure varied with ECC
level, and the hypothesis under investigation (a data-table error) cannot produce
an ECC-dependent failure. Checking the instrument is not paranoia; it is the
other half of the bisection.
