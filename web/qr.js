// qr.js — a dependency-free QR Code (Model 2) encoder, byte mode only.
//
// Why this file exists: the workshop projector shows a QR pointing at the
// server's LAN URL so ~30 attendees can reach the trader page on their phones.
// Generating that QR must not depend on npm, a CDN, or a third-party web
// service — the room may have no internet, and a build step is a demo blocker.
// So this is plain ES-module vanilla JS: drop it next to the other static
// files, `import { qrMatrix, qrSvg } from './qr.js'`, done.
//
// Scope: byte mode (8-bit) only. Numeric/alphanumeric/kanji modes would encode
// digit-only or uppercase-only payloads more densely, but our payload is always
// a mixed-case URL, which byte mode handles and the other modes cannot. Leaving
// them out removes ~40% of the code and every bug that could live in it.
// Versions 1..40 are supported; the smallest one that fits is chosen.
//
// Reference: ISO/IEC 18004. Section numbers in comments refer to it.

/* ------------------------------------------------------------------------ *
 * 1. Static tables
 * ------------------------------------------------------------------------ */

// Error-correction levels and their *format-information* bit values. Note the
// values are NOT 0,1,2,3 in level order: the spec assigns L=01, M=00, Q=11,
// H=10 (Table 12). Getting this wrong produces a QR that scanners reject
// outright, because the format bits tell the decoder which ECC math to run.
const ECC_FORMAT_BITS = { L: 1, M: 0, Q: 3, H: 2 };

// ECC codewords per block, indexed [level][version]. Index 0 is unused so the
// version number indexes directly. (Table 13-22.)
const ECC_CODEWORDS_PER_BLOCK = {
  L: [0, 7, 10, 15, 20, 26, 18, 20, 24, 30, 18, 20, 24, 26, 30, 22, 24, 28, 30, 28, 28, 28, 28, 30, 30, 26, 28, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30],
  M: [0, 10, 16, 26, 18, 24, 16, 18, 22, 22, 26, 30, 22, 22, 24, 24, 28, 28, 26, 26, 26, 26, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28],
  Q: [0, 13, 22, 18, 26, 18, 24, 18, 22, 20, 24, 28, 26, 24, 20, 30, 24, 28, 28, 26, 30, 28, 30, 30, 30, 30, 28, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30],
  H: [0, 17, 28, 22, 16, 22, 28, 26, 26, 24, 28, 24, 28, 22, 24, 24, 30, 28, 28, 26, 28, 30, 24, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30],
};

// Number of ECC blocks, indexed [level][version]. Above roughly 15 data
// codewords per block the Reed-Solomon decoder's correction power per block
// stops keeping up with burst damage (a coffee stain, a thumb, glare), so the
// spec splits the payload into several independently-corrected blocks. That
// split is the reason for the interleaving step further down.
const NUM_ECC_BLOCKS = {
  L: [0, 1, 1, 1, 1, 1, 2, 2, 2, 2, 4, 4, 4, 4, 4, 6, 6, 6, 6, 7, 8, 8, 9, 9, 10, 12, 12, 12, 13, 14, 15, 16, 17, 18, 19, 19, 20, 21, 22, 24, 25],
  M: [0, 1, 1, 1, 2, 2, 4, 4, 4, 5, 5, 5, 8, 9, 9, 10, 10, 11, 13, 14, 16, 17, 17, 18, 20, 21, 23, 25, 26, 28, 29, 31, 33, 35, 37, 38, 40, 43, 45, 47, 49],
  Q: [0, 1, 1, 2, 2, 4, 4, 6, 6, 8, 8, 8, 10, 12, 16, 12, 17, 16, 18, 21, 20, 23, 23, 25, 27, 29, 34, 34, 35, 38, 40, 43, 45, 48, 51, 53, 56, 59, 62, 65, 68],
  H: [0, 1, 1, 2, 4, 4, 4, 5, 6, 8, 8, 11, 11, 16, 16, 18, 16, 19, 21, 25, 25, 25, 34, 30, 32, 35, 37, 40, 42, 45, 48, 51, 54, 57, 60, 63, 66, 70, 74, 77, 81],
};

const MIN_VERSION = 1;
const MAX_VERSION = 40;

// Mask-pattern penalty weights (§7.8.3.1). Fixed by the spec, not tunable.
const PENALTY_N1 = 3;  // run of 5+ same-coloured modules in a row/column
const PENALTY_N2 = 3;  // 2x2 block of one colour
const PENALTY_N3 = 40; // finder-lookalike 1:1:3:1:1 pattern in the data
const PENALTY_N4 = 10; // per 5% that the dark/light balance deviates from 50%

/* ------------------------------------------------------------------------ *
 * 2. Geometry helpers (module counts, alignment patterns)
 * ------------------------------------------------------------------------ */

/** Side length in modules. v1 = 21x21, growing by 4 per version. */
function sizeForVersion(version) {
  return version * 4 + 17;
}

/**
 * Number of modules available to data+ECC+remainder bits, i.e. the whole
 * symbol minus every function pattern. Derived arithmetically rather than
 * table-looked-up so there is one less table to get wrong.
 */
function numRawDataModules(version) {
  // Total modules minus finders+separators+format info (the constant 64) and
  // minus the timing patterns, expressed as a quadratic in the version.
  let result = (16 * version + 128) * version + 64;
  if (version >= 2) {
    // Alignment patterns: numAlign^2 of them at 25 modules each, minus the
    // overlaps where they sit on the timing patterns, minus the three corner
    // slots that are occupied by finder patterns instead.
    const numAlign = Math.floor(version / 7) + 2;
    result -= (25 * numAlign - 10) * numAlign - 55;
    // Version 7+ additionally carries two 6x3 version-information blocks.
    if (version >= 7) result -= 36;
  }
  return result;
}

/** Data codewords available after ECC is reserved. */
function numDataCodewords(version, ecc) {
  const totalCodewords = Math.floor(numRawDataModules(version) / 8);
  return totalCodewords - ECC_CODEWORDS_PER_BLOCK[ecc][version] * NUM_ECC_BLOCKS[ecc][version];
}

/**
 * Centre coordinates of the alignment patterns (§6.3.5, Annex E).
 * The first is always 6, the last always size-7, and the intermediate ones are
 * spaced as evenly as possible with an even step. Version 32 is the one case
 * where the general formula disagrees with the published table, so it is
 * special-cased — a genuine wart in the standard, not in this code.
 *
 * Worth knowing if you ever test against jsQR 1.4.0: its table has version 23
 * as [6,30,54,74,102], reusing version 22's fourth centre. The spec value is
 * 78 (uniform 24-module gaps). We keep 78; the test harness proves the point
 * differentially rather than bending the encoder to match a buggy decoder.
 */
function alignmentPatternPositions(version) {
  if (version === 1) return [];
  const numAlign = Math.floor(version / 7) + 2;
  const step = version === 32
    ? 26
    : Math.ceil((version * 4 + 4) / (numAlign * 2 - 2)) * 2;
  const result = [6];
  for (let pos = sizeForVersion(version) - 7; result.length < numAlign; pos -= step) {
    result.splice(1, 0, pos);
  }
  return result;
}

/** Byte-mode character-count indicator width (§8.4, Table 3). */
function charCountBits(version) {
  return version <= 9 ? 8 : 16;
}

/* ------------------------------------------------------------------------ *
 * 3. Text -> bytes
 * ------------------------------------------------------------------------ */

/**
 * UTF-8 encode, done by hand so this file has no dependency on TextEncoder
 * either (it exists everywhere modern, but hand-rolling it is 20 lines and
 * makes the module work in any JS host, including a plain <script type=module>
 * in an ancient WebView on someone's phone).
 *
 * Note on ECI: the spec's byte mode nominally means ISO-8859-1, and strictly
 * signalling UTF-8 requires an ECI header (mode 0111, assignment 26). We do
 * not emit one, deliberately: pure ASCII (every URL we care about) is identical
 * in both charsets, and for non-ASCII every scanner in practice — and jsQR,
 * zxing, and both phone cameras — auto-detects UTF-8 byte sequences, while a
 * meaningful minority of older readers choke on an ECI header. Emitting raw
 * UTF-8 is the pragmatic, widely-deployed choice; it is a conscious deviation,
 * not an oversight.
 */
function utf8Bytes(text) {
  const out = [];
  for (let i = 0; i < text.length; i++) {
    let cp = text.codePointAt(i);
    if (cp > 0xFFFF) i++; // consume the low surrogate of a surrogate pair
    if (cp < 0x80) {
      out.push(cp);
    } else if (cp < 0x800) {
      out.push(0xC0 | (cp >> 6), 0x80 | (cp & 0x3F));
    } else if (cp < 0x10000) {
      out.push(0xE0 | (cp >> 12), 0x80 | ((cp >> 6) & 0x3F), 0x80 | (cp & 0x3F));
    } else {
      out.push(
        0xF0 | (cp >> 18),
        0x80 | ((cp >> 12) & 0x3F),
        0x80 | ((cp >> 6) & 0x3F),
        0x80 | (cp & 0x3F),
      );
    }
  }
  return out;
}

/* ------------------------------------------------------------------------ *
 * 4. Bit buffer
 * ------------------------------------------------------------------------ */

/** Append the low `len` bits of `value`, most-significant bit first. */
function appendBits(bits, value, len) {
  for (let i = len - 1; i >= 0; i--) bits.push((value >>> i) & 1);
}

/* ------------------------------------------------------------------------ *
 * 5. GF(256) arithmetic and Reed-Solomon
 * ------------------------------------------------------------------------ */

// QR uses GF(2^8) with primitive polynomial x^8+x^4+x^3+x^2+1 = 0x11D.
// Multiplication is done with log/antilog tables: a*b = exp[log[a]+log[b]].
const GF_EXP = new Uint8Array(512);
const GF_LOG = new Uint8Array(256);
(function buildGaloisTables() {
  let x = 1;
  for (let i = 0; i < 255; i++) {
    GF_EXP[i] = x;
    GF_LOG[x] = i;
    x <<= 1;
    if (x & 0x100) x ^= 0x11D; // reduce modulo the primitive polynomial
  }
  // Duplicate the table so log sums up to 508 can be indexed without a modulo.
  for (let i = 255; i < 512; i++) GF_EXP[i] = GF_EXP[i - 255];
})();

function gfMul(a, b) {
  if (a === 0 || b === 0) return 0;
  return GF_EXP[GF_LOG[a] + GF_LOG[b]];
}

/**
 * Generator polynomial for `degree` ECC codewords: the product of
 * (x - a^0)(x - a^1)...(x - a^(degree-1)). Returned coefficients are in
 * descending order of power, with the implicit leading 1 omitted.
 */
function rsGeneratorPoly(degree) {
  let poly = [1];
  for (let i = 0; i < degree; i++) {
    // Multiply poly by (x - a^i). In GF(2^n) subtraction is XOR, so the sign
    // of a^i is irrelevant.
    const root = GF_EXP[i];
    const next = new Array(poly.length + 1).fill(0);
    for (let j = 0; j < poly.length; j++) {
      next[j] ^= poly[j];
      next[j + 1] ^= gfMul(poly[j], root);
    }
    poly = next;
  }
  return poly.slice(1); // drop the leading 1
}

/**
 * Reed-Solomon ECC codewords for one block: the remainder of
 * data(x) * x^degree divided by the generator polynomial. Implemented as
 * synthetic division over a sliding remainder register, which is the same
 * computation a hardware LFSR would do.
 */
function rsEncode(data, degree, generator) {
  const remainder = new Array(degree).fill(0);
  for (const byte of data) {
    const factor = byte ^ remainder.shift();
    remainder.push(0);
    for (let i = 0; i < degree; i++) {
      remainder[i] ^= gfMul(generator[i], factor);
    }
  }
  return remainder;
}

/* ------------------------------------------------------------------------ *
 * 6. Encoding: text -> final interleaved codeword stream
 * ------------------------------------------------------------------------ */

/** Smallest version that can hold `byteLen` bytes at this ECC level. */
function chooseVersion(byteLen, ecc) {
  for (let version = MIN_VERSION; version <= MAX_VERSION; version++) {
    const capacityBits = numDataCodewords(version, ecc) * 8;
    // 4 mode bits + the character-count indicator + the payload itself.
    const neededBits = 4 + charCountBits(version) + byteLen * 8;
    if (neededBits <= capacityBits) return version;
  }
  throw new RangeError(
    `data too long for a QR code: ${byteLen} bytes at ECC level ${ecc} exceeds version 40`,
  );
}

/** Build the padded data codewords for one symbol (§8.4.2, §8.4.9). */
function buildDataCodewords(bytes, version, ecc) {
  const capacityBits = numDataCodewords(version, ecc) * 8;
  const bits = [];

  appendBits(bits, 0b0100, 4);                       // byte-mode indicator
  appendBits(bits, bytes.length, charCountBits(version));
  for (const b of bytes) appendBits(bits, b, 8);

  // Terminator: up to four 0 bits, truncated if the symbol is nearly full.
  appendBits(bits, 0, Math.min(4, capacityBits - bits.length));
  // Pad to a byte boundary.
  appendBits(bits, 0, (8 - (bits.length % 8)) % 8);

  // Fill the remaining capacity with the spec's alternating pad bytes
  // 11101100 / 00010001. They are not zeros on purpose: a long run of one
  // value would bias the mask-penalty scoring and can create finder-lookalike
  // patterns, so the spec picked a visually noisy alternation.
  const codewords = [];
  for (let i = 0; i < bits.length; i += 8) {
    let byte = 0;
    for (let j = 0; j < 8; j++) byte = (byte << 1) | bits[i + j];
    codewords.push(byte);
  }
  for (let pad = 0xEC; codewords.length < capacityBits / 8; pad ^= 0xEC ^ 0x11) {
    codewords.push(pad);
  }
  return codewords;
}

/**
 * Split the data into blocks, compute per-block ECC, and interleave.
 *
 * Why interleave at all: physical damage to a QR is spatially local (a smudge,
 * a fold, a finger). If block 1's codewords all sat together in one corner,
 * a smudge over that corner would blow past block 1's correction capacity and
 * the symbol would be unreadable even though blocks 2..n were untouched. By
 * taking one codeword from each block in turn, any local damage is spread
 * thinly across all blocks, so each block only has to correct a few errors.
 *
 * This is the single most common place a hand-rolled encoder is silently
 * wrong: single-block versions (1..4 at most levels) work fine without any of
 * this, so the bug only shows up at version 5+ where blocks appear, and even
 * then only when short and long blocks are mixed.
 */
function interleaveBlocks(data, version, ecc) {
  const numBlocks = NUM_ECC_BLOCKS[ecc][version];
  const eccLen = ECC_CODEWORDS_PER_BLOCK[ecc][version];
  const totalCodewords = Math.floor(numRawDataModules(version) / 8);

  // The data is divided as evenly as possible: `numShort` blocks of the base
  // size, then the rest one codeword longer. Every block gets the SAME number
  // of ECC codewords regardless.
  const numShort = numBlocks - (totalCodewords % numBlocks);
  const shortDataLen = Math.floor(totalCodewords / numBlocks) - eccLen;

  const generator = rsGeneratorPoly(eccLen);
  const dataBlocks = [];
  const eccBlocks = [];
  let offset = 0;
  for (let i = 0; i < numBlocks; i++) {
    const len = shortDataLen + (i < numShort ? 0 : 1);
    const block = data.slice(offset, offset + len);
    offset += len;
    dataBlocks.push(block);
    eccBlocks.push(rsEncode(block, eccLen, generator));
  }

  const result = [];
  // Data codewords first, column-major across blocks. Short blocks simply have
  // nothing to contribute at the final index — that hole, not a padding byte,
  // is what the "skip" below represents.
  const maxDataLen = shortDataLen + 1;
  for (let i = 0; i < maxDataLen; i++) {
    for (const block of dataBlocks) {
      if (i < block.length) result.push(block[i]);
    }
  }
  // Then all ECC codewords, likewise interleaved. These are never ragged.
  for (let i = 0; i < eccLen; i++) {
    for (const block of eccBlocks) result.push(block[i]);
  }
  return result;
}

/* ------------------------------------------------------------------------ *
 * 7. Symbol construction
 * ------------------------------------------------------------------------ */

/**
 * A symbol under construction: the module colours plus a parallel map marking
 * which modules are function patterns. The second map matters because masking
 * and data placement must touch ONLY the data region — masking a finder
 * pattern would destroy the decoder's ability to locate the symbol at all.
 */
function newSymbol(version) {
  const size = sizeForVersion(version);
  const modules = [];
  const isFunction = [];
  for (let y = 0; y < size; y++) {
    modules.push(new Array(size).fill(false));
    isFunction.push(new Array(size).fill(false));
  }
  return { version, size, modules, isFunction };
}

function setFunctionModule(sym, x, y, dark) {
  sym.modules[y][x] = dark;
  sym.isFunction[y][x] = true;
}

/** 7x7 finder + its 1-module light separator, anchored at a corner. */
function drawFinderPattern(sym, cx, cy) {
  // Iterate a 9x9 neighbourhood so the separator is drawn in the same pass.
  // Chebyshev distance from the centre gives the concentric ring structure:
  // rings 0,1 dark (3x3 core), ring 2 light, ring 3 dark (7x7 border),
  // ring 4 light (the separator).
  for (let dy = -4; dy <= 4; dy++) {
    for (let dx = -4; dx <= 4; dx++) {
      const dist = Math.max(Math.abs(dx), Math.abs(dy));
      const x = cx + dx;
      const y = cy + dy;
      if (x >= 0 && x < sym.size && y >= 0 && y < sym.size) {
        setFunctionModule(sym, x, y, dist !== 2 && dist !== 4);
      }
    }
  }
}

/** 5x5 alignment pattern centred at (cx, cy). */
function drawAlignmentPattern(sym, cx, cy) {
  for (let dy = -2; dy <= 2; dy++) {
    for (let dx = -2; dx <= 2; dx++) {
      setFunctionModule(sym, cx + dx, cy + dy, Math.max(Math.abs(dx), Math.abs(dy)) !== 1);
    }
  }
}

function drawFunctionPatterns(sym) {
  const size = sym.size;

  // Timing patterns: alternating modules on row 6 and column 6. They give the
  // decoder a module-pitch reference so it can resample a perspective-warped
  // photo back onto the module grid.
  for (let i = 0; i < size; i++) {
    setFunctionModule(sym, 6, i, i % 2 === 0);
    setFunctionModule(sym, i, 6, i % 2 === 0);
  }

  // Three finders. The fourth corner is deliberately empty — that asymmetry is
  // how a decoder determines the symbol's rotation.
  drawFinderPattern(sym, 3, 3);
  drawFinderPattern(sym, size - 4, 3);
  drawFinderPattern(sym, 3, size - 4);

  // Alignment patterns everywhere except where they would collide with a
  // finder (the three corner combinations of first/last index).
  const positions = alignmentPatternPositions(sym.version);
  const n = positions.length;
  for (let i = 0; i < n; i++) {
    for (let j = 0; j < n; j++) {
      const corner = (i === 0 && j === 0) || (i === 0 && j === n - 1) || (i === n - 1 && j === 0);
      if (!corner) drawAlignmentPattern(sym, positions[i], positions[j]);
    }
  }

  // Reserve the format-information area with a dummy value; the real bits are
  // written after the mask is chosen (they encode the mask number).
  drawFormatBits(sym, 'M', 0);
  drawVersionBits(sym);
}

/**
 * Format information: 5 bits (2 ECC level + 3 mask) expanded to 15 by a
 * BCH(15,5) code, then XORed with 0x5412.
 *
 * Why the XOR mask: without it, the all-zero data word (ECC level M, mask 0)
 * would produce 15 zero bits — a solid light block sitting right next to the
 * finder patterns, which both looks like a quiet zone and gives the decoder no
 * signal to lock onto. XORing with a fixed, deliberately mixed constant
 * guarantees the format area is never uniform for any of the 32 possible
 * values. Same reasoning as the pad bytes above.
 *
 * The information is written twice, in two physically separate places, because
 * it is unprotected by the main Reed-Solomon code: if you cannot read the
 * format bits you cannot decode anything else, so it gets its own redundancy.
 */
function drawFormatBits(sym, ecc, mask) {
  const data = (ECC_FORMAT_BITS[ecc] << 3) | mask;
  let rem = data;
  // Append 10 BCH check bits using generator polynomial 0x537.
  for (let i = 0; i < 10; i++) rem = (rem << 1) ^ ((rem >>> 9) * 0x537);
  const bits = ((data << 10) | rem) ^ 0x5412;

  const size = sym.size;
  const bit = (i) => ((bits >>> i) & 1) !== 0;

  // Copy 1: around the top-left finder, skipping the timing pattern at index 6.
  for (let i = 0; i <= 5; i++) setFunctionModule(sym, 8, i, bit(i));
  setFunctionModule(sym, 8, 7, bit(6));
  setFunctionModule(sym, 8, 8, bit(7));
  setFunctionModule(sym, 7, 8, bit(8));
  for (let i = 9; i < 15; i++) setFunctionModule(sym, 14 - i, 8, bit(i));

  // Copy 2: split between the bottom-left and top-right finders.
  for (let i = 0; i < 8; i++) setFunctionModule(sym, size - 1 - i, 8, bit(i));
  for (let i = 8; i < 15; i++) setFunctionModule(sym, 8, size - 15 + i, bit(i));

  // The "dark module" — always dark, at a fixed offset above the bottom-left
  // format copy. It is a fixed reference point, not data.
  setFunctionModule(sym, 8, size - 8, true);
}

/**
 * Version information, versions 7+ only: 6 version bits plus 12 BCH check bits
 * (generator 0x1F25), written twice near the top-right and bottom-left finders.
 * Below version 7 the decoder infers the version from the symbol's measured
 * size; from version 7 the symbols are large enough that a miscount of the
 * module pitch becomes plausible, so the version is stated explicitly.
 */
function drawVersionBits(sym) {
  if (sym.version < 7) return;
  let rem = sym.version;
  for (let i = 0; i < 12; i++) rem = (rem << 1) ^ ((rem >>> 11) * 0x1F25);
  const bits = (sym.version << 12) | rem;

  for (let i = 0; i < 18; i++) {
    const dark = ((bits >>> i) & 1) !== 0;
    const a = sym.size - 11 + (i % 3);
    const b = Math.floor(i / 3);
    setFunctionModule(sym, a, b, dark);  // top-right block
    setFunctionModule(sym, b, a, dark);  // bottom-left block (transposed)
  }
}

/**
 * Lay the codeword bits into the data region: two-module-wide columns walked
 * from the bottom-right corner, zigzagging up and down, right-to-left, skipping
 * function modules. Column 6 is skipped entirely because it is the vertical
 * timing pattern.
 *
 * Any leftover "remainder bits" (the symbol capacity is not always a whole
 * number of codewords) stay light here and are then masked like data, which is
 * exactly what the spec requires.
 */
function drawCodewords(sym, codewords) {
  let i = 0; // bit index into the codeword stream
  for (let right = sym.size - 1; right >= 1; right -= 2) {
    if (right === 6) right = 5;
    for (let vert = 0; vert < sym.size; vert++) {
      for (let j = 0; j < 2; j++) {
        const x = right - j;
        const upward = ((right + 1) & 2) === 0;
        const y = upward ? sym.size - 1 - vert : vert;
        if (!sym.isFunction[y][x] && i < codewords.length * 8) {
          sym.modules[y][x] = ((codewords[i >>> 3] >>> (7 - (i & 7))) & 1) !== 0;
          i++;
        }
      }
    }
  }
}

/**
 * The 8 data mask patterns (§7.8.2). A mask XORs a regular pattern over the
 * data region only, to break up large uniform areas and accidental
 * finder-lookalikes that would otherwise confuse a decoder. The mask number is
 * recorded in the format bits, so this is fully reversible.
 */
function maskAt(mask, x, y) {
  switch (mask) {
    case 0: return (x + y) % 2 === 0;
    case 1: return y % 2 === 0;
    case 2: return x % 3 === 0;
    case 3: return (x + y) % 3 === 0;
    case 4: return (Math.floor(y / 2) + Math.floor(x / 3)) % 2 === 0;
    case 5: return ((x * y) % 2) + ((x * y) % 3) === 0;
    case 6: return (((x * y) % 2) + ((x * y) % 3)) % 2 === 0;
    case 7: return (((x + y) % 2) + ((x * y) % 3)) % 2 === 0;
    default: throw new RangeError('mask out of range');
  }
}

/** XOR a mask over the data region. Applying it twice undoes it. */
function applyMask(sym, mask) {
  for (let y = 0; y < sym.size; y++) {
    for (let x = 0; x < sym.size; x++) {
      if (!sym.isFunction[y][x] && maskAt(mask, x, y)) {
        sym.modules[y][x] = !sym.modules[y][x];
      }
    }
  }
}

/**
 * Penalty score for a masked symbol (§7.8.3.1). The mask with the LOWEST score
 * wins. The four rules each penalise a specific way a symbol can be hard to
 * read: long same-colour runs (hard to count modules), solid blocks (same),
 * finder-lookalike sequences (false position detection), and an unbalanced
 * dark/light ratio (poor contrast decisions under a camera's auto-exposure).
 */
function penaltyScore(sym) {
  const size = sym.size;
  const m = sym.modules;
  let score = 0;

  // N1: runs of five or more identical modules, horizontally and vertically.
  for (let y = 0; y < size; y++) {
    let runColor = m[y][0], runLen = 1;
    for (let x = 1; x < size; x++) {
      if (m[y][x] === runColor) {
        runLen++;
      } else {
        if (runLen >= 5) score += PENALTY_N1 + (runLen - 5);
        runColor = m[y][x];
        runLen = 1;
      }
    }
    if (runLen >= 5) score += PENALTY_N1 + (runLen - 5);
  }
  for (let x = 0; x < size; x++) {
    let runColor = m[0][x], runLen = 1;
    for (let y = 1; y < size; y++) {
      if (m[y][x] === runColor) {
        runLen++;
      } else {
        if (runLen >= 5) score += PENALTY_N1 + (runLen - 5);
        runColor = m[y][x];
        runLen = 1;
      }
    }
    if (runLen >= 5) score += PENALTY_N1 + (runLen - 5);
  }

  // N2: every 2x2 block of a single colour. Overlapping blocks each count,
  // so a solid 3x3 area scores four times.
  for (let y = 0; y < size - 1; y++) {
    for (let x = 0; x < size - 1; x++) {
      const c = m[y][x];
      if (c === m[y][x + 1] && c === m[y + 1][x] && c === m[y + 1][x + 1]) {
        score += PENALTY_N2;
      }
    }
  }

  // N3: the 1:1:3:1:1 finder ratio (dark-light-dark^3-light-dark) with four
  // light modules on either side. Rows and columns are padded with four light
  // modules at each end so a pattern touching the symbol edge — where the
  // quiet zone supplies the light run — is still caught.
  const FINDER = [true, false, true, true, true, false, true, false, false, false, false];
  const matches = (line, at, pattern) => {
    for (let k = 0; k < pattern.length; k++) {
      if (line[at + k] !== pattern[k]) return false;
    }
    return true;
  };
  const REVERSED = FINDER.slice().reverse();
  const pad = [false, false, false, false];
  for (let y = 0; y < size; y++) {
    const line = pad.concat(m[y], pad);
    for (let x = 0; x + 11 <= line.length; x++) {
      if (matches(line, x, FINDER) || matches(line, x, REVERSED)) score += PENALTY_N3;
    }
  }
  for (let x = 0; x < size; x++) {
    const col = [];
    for (let y = 0; y < size; y++) col.push(m[y][x]);
    const line = pad.concat(col, pad);
    for (let y = 0; y + 11 <= line.length; y++) {
      if (matches(line, y, FINDER) || matches(line, y, REVERSED)) score += PENALTY_N3;
    }
  }

  // N4: 10 points per full 5% that the proportion of dark modules deviates
  // from 50%.
  let dark = 0;
  for (let y = 0; y < size; y++) {
    for (let x = 0; x < size; x++) if (m[y][x]) dark++;
  }
  const total = size * size;
  const deviation = Math.abs(dark * 100 - total * 50); // = |percent-50| * total
  score += Math.floor(deviation / (5 * total)) * PENALTY_N4;

  return score;
}

/* ------------------------------------------------------------------------ *
 * 8. Public API
 * ------------------------------------------------------------------------ */

/**
 * Encode `text` as a QR code.
 *
 * @param {string} text  payload; encoded as UTF-8 bytes in byte mode.
 * @param {{ecc?: 'L'|'M'|'Q'|'H', version?: number, mask?: number}} [opts]
 *        ecc defaults to 'M' (~15% recoverable), the usual choice for a
 *        projected/printed URL. `version` forces a minimum version and `mask`
 *        forces a mask; both exist for testing and are normally left alone.
 * @returns {{size: number, modules: boolean[][], version: number, ecc: string, mask: number}}
 *        `modules[y][x] === true` means a dark module. No quiet zone is
 *        included — callers must add at least 4 light modules on every side or
 *        scanners will struggle.
 */
export function qrMatrix(text, opts) {
  const options = opts || {};
  const ecc = options.ecc || 'M';
  if (!Object.prototype.hasOwnProperty.call(ECC_FORMAT_BITS, ecc)) {
    throw new RangeError(`unknown ECC level ${ecc}; expected one of L, M, Q, H`);
  }
  if (typeof text !== 'string') throw new TypeError('qrMatrix expects a string');

  const bytes = utf8Bytes(text);
  let version = chooseVersion(bytes.length, ecc);
  if (options.version != null) {
    if (options.version < MIN_VERSION || options.version > MAX_VERSION) {
      throw new RangeError('version must be 1..40');
    }
    if (options.version < version) {
      throw new RangeError(`data does not fit in version ${options.version} at ECC ${ecc}`);
    }
    version = options.version;
  }

  const data = buildDataCodewords(bytes, version, ecc);
  const codewords = interleaveBlocks(data, version, ecc);

  const sym = newSymbol(version);
  drawFunctionPatterns(sym);
  drawCodewords(sym, codewords);

  // Try every mask and keep the lowest-penalty one. The masks are applied and
  // un-applied in place (XOR is its own inverse), so only one matrix is ever
  // allocated.
  let bestMask = options.mask != null ? options.mask : -1;
  if (bestMask < 0) {
    let bestScore = Infinity;
    for (let mask = 0; mask < 8; mask++) {
      applyMask(sym, mask);
      drawFormatBits(sym, ecc, mask); // format bits are part of the scored image
      const score = penaltyScore(sym);
      if (score < bestScore) {
        bestScore = score;
        bestMask = mask;
      }
      applyMask(sym, mask); // undo
    }
  }
  applyMask(sym, bestMask);
  drawFormatBits(sym, ecc, bestMask);

  return { size: sym.size, modules: sym.modules, version, ecc, mask: bestMask };
}

/**
 * Render `text` as standalone SVG markup — used for the docs/ printout and the
 * projector's idle screen. Self-contained: no CSS, no external refs, so it can
 * be inlined in a page, written to a file, or opened directly in a browser.
 *
 * @param {string} text
 * @param {{ecc?: 'L'|'M'|'Q'|'H', moduleSize?: number, margin?: number,
 *          dark?: string, light?: string}} [opts]
 *        moduleSize: pixels per module (default 8).
 *        margin: quiet zone in MODULES (default 4 — the spec minimum; going
 *        below this is the most common reason a valid QR will not scan).
 */
export function qrSvg(text, opts) {
  const options = opts || {};
  const moduleSize = options.moduleSize != null ? options.moduleSize : 8;
  const margin = options.margin != null ? options.margin : 4;
  const dark = options.dark || '#000000';
  const light = options.light || '#ffffff';
  if (margin < 4) {
    // Not fatal — some callers legitimately want a tight crop and add their own
    // white surround in CSS — but worth saying out loud.
    console.warn('qrSvg: quiet zone below the spec minimum of 4 modules; scanners may fail');
  }

  const { size, modules } = qrMatrix(text, options);
  const dim = (size + margin * 2) * moduleSize;

  // One path with many subpaths rather than thousands of <rect>s: smaller
  // output and far fewer DOM nodes when inlined. Horizontal runs are merged so
  // a row of dark modules becomes a single wide rectangle.
  const parts = [];
  for (let y = 0; y < size; y++) {
    let x = 0;
    while (x < size) {
      if (!modules[y][x]) { x++; continue; }
      let run = 1;
      while (x + run < size && modules[y][x + run]) run++;
      const px = (x + margin) * moduleSize;
      const py = (y + margin) * moduleSize;
      parts.push(`M${px} ${py}h${run * moduleSize}v${moduleSize}h-${run * moduleSize}z`);
      x += run;
    }
  }

  return [
    '<?xml version="1.0" encoding="UTF-8"?>',
    `<svg xmlns="http://www.w3.org/2000/svg" version="1.1" width="${dim}" height="${dim}" ` +
      `viewBox="0 0 ${dim} ${dim}" shape-rendering="crispEdges">`,
    // The quiet zone is drawn, not assumed: an SVG on a dark page with a
    // transparent background is unscannable.
    `<rect width="${dim}" height="${dim}" fill="${light}"/>`,
    `<path d="${parts.join('')}" fill="${dark}"/>`,
    '</svg>',
    '',
  ].join('\n');
}
