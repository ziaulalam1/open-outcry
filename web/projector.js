// projector.js — the screen that gets photographed.
//
// Two rules govern everything in this file:
//
//  1. INGEST AND RENDER ARE SEPARATE. applyFrame() always updates state, even
//     while frozen. Freeze stops render() being called; it never stops the feed.
//     That is what makes "freeze affects rendering only" literally true, and it
//     is demonstrable on stage: freeze, talk for ten seconds, unfreeze, and the
//     book has moved on without you.
//
//  2. DIVERGENCE IS COMPUTED HERE, NOT ON THE SERVER. Divergence is a property
//     of two *rendered* frames and only the browser holds both. A frame can be
//     lost after the server's WriteMessage returns, so any server-side claim
//     about what the stale pane is showing would sometimes be a lie. Computing
//     it server-side would also mean modelling the stale client's delivery
//     state — a shadow projection, and exactly the contamination the chaos
//     boundary forbids.

const CHECKS = [
  ['conservation', 'CONSERVATION'],
  ['no_cross', 'NO CROSSED BOOK'],
  ['priority', 'PRICE-TIME PRIORITY'],
  ['sweep', 'SWEEP COMPLETE'],
]

const el = (tag, cls, txt) => {
  const n = document.createElement(tag)
  if (cls) n.className = cls
  if (txt != null) n.textContent = txt
  return n
}
const money = (cents, scale) => (cents / scale).toFixed(2)
const num = (n) => n.toLocaleString('en-US')

// Ladder depth bounds. The upper bound is taste; the lower bound is the point
// below which the book stops looking like a book in a photograph.
// A shallow book still reads as a book in a photograph. A clipped one does not,
// so when something has to give it is depth, never the top or bottom row.
const MAX_LEVELS = 7
const MIN_LEVELS = 3

// The room grid is bounded. Thirty phones is the expected case; an unbounded
// grid would wrap and eat the ladder's height the moment someone opened a
// second tab per person.
const MAX_CELLS = 40

export function mountProjector(root, { hero = false, showGrid = true, idle: forceIdle = false } = {}) {
  if (hero) document.body.classList.add('hero')
  document.documentElement.style.setProperty('--s', hero ? '1.3' : '1')

  // ── state ───────────────────────────────────────────────────────────────────
  const st = {
    panes: { fresh: null, stale: null },
    stats: null,
    meta: {},
    split: false,
    halt: null,
    lastGood: null,          // kept so a halted book still has something to show
    sessions: new Map(),     // id -> last activity (ms, tape/engine clock)
    clock: 0,
    live: false,
    fitKey: null,            // layout signature the current depth was fitted for
    levels: MAX_LEVELS,
  }
  addEventListener('resize', () => { st.fitKey = null; schedule() })
  let frozen = false
  let dirty = false
  const hotTimers = new Map()

  // ── DOM skeleton ────────────────────────────────────────────────────────────
  root.innerHTML = ''
  const wrap = el('div', 'proj')

  const bar = el('div', 'bar')
  const h1 = el('h1', null, 'OPEN OUTCRY')
  const sub = el('div', 'sub', 'Long Island University · Live Order Book Workshop')
  const right = el('div', 'right')
  const live = el('div', 'live off')
  const dot = el('div', 'dot')
  const liveTxt = el('span', null, 'CONNECTING')
  live.append(dot, liveTxt)
  const count = el('div', 'count')
  right.append(live, count)
  bar.append(h1, sub, right)

  const banner = el('div', 'banner hidden')

  const panes = el('div', 'panes')
  const mk = (kind, tag) => {
    const p = el('div', `pane ${kind}`)
    const head = el('header')
    const t = el('div', 'tag', tag)
    const lag = el('div', 'lag')
    head.append(t, lag)
    const ladder = el('div', 'ladder')
    p.append(head, ladder)
    return { root: p, ladder, lag, head }
  }
  const freshPane = mk('fresh', 'LIVE FEED')
  const stalePane = mk('stale', 'DEGRADED FEED')
  stalePane.root.classList.add('hidden')
  panes.append(freshPane.root, stalePane.root)

  const foot = el('div', 'foot')
  const inv = el('div', 'inv')
  const stats = el('div', 'stats')
  foot.append(inv, stats)

  const gridWrap = el('div', 'grid')
  if (showGrid) { gridWrap.append(el('div', 'lbl', 'Room')) }

  wrap.append(bar, banner, panes, foot)
  if (showGrid) wrap.append(gridWrap)
  root.append(wrap)

  const idle = el('div', 'idle')
  const idleQr = el('div', 'qr')
  idle.append(el('h2', null, 'Scan to trade'), idleQr, el('div', 'url'), el('div', 'hint', 'Open Outcry · no app, no login'))
  root.append(idle)

  const halt = el('div', 'halt hidden')
  root.append(halt)
  root.append(el('div', 'frozen-edge'))
  root.append(el('div', 'freeze-tag', 'FRAME HELD · ENGINE STILL RUNNING'))

  // ── QR: how thirty phones actually reach the app ────────────────────────────
  // Dynamically imported so a missing/failed encoder degrades to a readable URL
  // rather than a blank projector. The URL is supplied by the server, which is
  // the only party that knows which LAN address it is actually reachable on.
  async function renderQR(url) {
    idle.querySelector('.url').textContent = url
    try {
      const { qrMatrix } = await import('./qr.js')
      const { size, modules } = qrMatrix(url, { ecc: 'M' })
      const px = 8
      const quiet = 4
      const c = document.createElement('canvas')
      c.width = c.height = (size + quiet * 2) * px
      const g = c.getContext('2d')
      g.fillStyle = '#fff'; g.fillRect(0, 0, c.width, c.height)
      g.fillStyle = '#000'
      for (let y = 0; y < size; y++) {
        for (let x = 0; x < size; x++) {
          if (modules[y][x]) g.fillRect((x + quiet) * px, (y + quiet) * px, px, px)
        }
      }
      idleQr.innerHTML = ''
      idleQr.append(c)
    } catch (err) {
      idleQr.innerHTML = ''
      idleQr.append(el('div', null, 'QR unavailable'))
      console.warn('qr encoder unavailable:', err)
    }
  }

  // ── ladder rendering ────────────────────────────────────────────────────────
  const maxQty = (rows) => Math.max(1, ...rows.map((r) => r[1]))

  function row(price, qty, orders, side, scale, cls, delta) {
    const r = el('div', `row ${side}${cls ? ' ' + cls : ''}`)
    const bar = el('div', 'bar')
    r.append(bar)
    r.append(el('div', 'px', money(price, scale)))
    r.append(el('div', 'qty', qty == null ? '—' : num(qty)))
    if (delta != null) r.append(el('div', 'delta', (delta > 0 ? '+' : '') + num(delta)))
    else r.append(el('div', 'n', orders == null ? '' : `×${orders}`))
    return { node: r, bar }
  }

  // Render one pane. When `against` is supplied (the stale pane against the
  // fresh one) each price level is classified and the difference is drawn.
  function renderLadder(target, frame, against, scale, levels) {
    target.ladder.innerHTML = ''
    if (!frame) return

    const build = (side) => {
      const mine = new Map(frame[side].map((r) => [r[0], r]))
      const theirs = against ? new Map(against[side].map((r) => [r[0], r])) : null
      const prices = new Set([...mine.keys(), ...(theirs ? theirs.keys() : [])])
      const desc = side === 'bids'
      const sorted = [...prices].sort((a, b) => (desc ? b - a : a - b)).slice(0, levels)
      return sorted.map((p) => {
        const m = mine.get(p)
        const t = theirs ? theirs.get(p) : null
        if (!theirs) return { p, qty: m[1], orders: m[2], cls: '' }
        if (m && t) {
          return m[1] === t[1]
            ? { p, qty: m[1], orders: m[2], cls: '' }
            : { p, qty: m[1], orders: m[2], cls: 'diverged', delta: m[1] - t[1] }
        }
        // present in fresh, absent here: the SHAPE of the hole must show.
        if (!m && t) return { p, qty: null, orders: null, cls: 'ghost' }
        // present here, already consumed in fresh: a phantom level.
        return { p, qty: m[1], orders: m[2], cls: 'phantom' }
      })
    }

    const askRows = build('asks')
    const bidRows = build('bids')
    const scaleQty = maxQty([...askRows, ...bidRows].filter((r) => r.qty).map((r) => [r.p, r.qty]))

    // Asks descend to the best ask at the bottom, bids descend from the best bid
    // at the top — the spread sits in the middle, where the room looks.
    const asksTopDown = askRows.slice().reverse()
    asksTopDown.forEach((r, i) => {
      const isBest = i === asksTopDown.length - 1 && r.cls !== 'ghost'
      const { node, bar } = row(r.p, r.qty, r.orders, 'ask', scale, (isBest ? 'best ' : '') + r.cls, r.delta)
      bar.style.width = `${Math.min(100, ((r.qty || 0) / scaleQty) * 100)}%`
      target.ladder.append(node)
    })

    const bestAsk = askRows.find((r) => r.qty != null)
    const bestBid = bidRows.find((r) => r.qty != null)
    const sp = el('div', 'spread')
    sp.append(el('span', null, 'SPREAD'))
    const spb = el('b', null, bestAsk && bestBid ? money(bestAsk.p - bestBid.p, scale) : '—')
    sp.append(spb)
    if (frame.has_last && frame.last) {
      sp.append(el('span', null, `LAST ${money(frame.last.px, scale)} × ${num(frame.last.qty)}`))
    }
    target.ladder.append(sp)

    bidRows.forEach((r, i) => {
      const isBest = i === 0 && r.cls !== 'ghost'
      const { node, bar } = row(r.p, r.qty, r.orders, 'bid', scale, (isBest ? 'best ' : '') + r.cls, r.delta)
      bar.style.width = `${Math.min(100, ((r.qty || 0) / scaleQty) * 100)}%`
      target.ladder.append(node)
    })
  }

  // ── the banner: plain English, templated here from structured facts ─────────
  // The server ships numbers. The screen owns its words.
  function bannerText(lagMs, lagFrames, dropped) {
    if (!st.split) return null
    if (lagMs < 250 && !dropped) {
      return 'Same book, two feeds. Watch the right-hand side start to fall behind.'
    }
    const s = (lagMs / 1000).toFixed(1)
    const missing = dropped > 0 ? ` ${num(dropped)} updates never arrived.` : ''
    return `The right-hand book is ${s} seconds behind.${missing} Both books are correct — only delivery degraded.`
  }

  // ── render ──────────────────────────────────────────────────────────────────
  function render() {
    dirty = false
    const f = st.panes.fresh
    const s = st.panes.stale
    const scale = (f && f.px_scale) || (s && s.px_scale) || 100

    // Idle screen until the first book frame arrives. &idle=1 pins it there:
    // on the day, the QR is on the projector while the room fills up, and the
    // book should not start until the presenter says so.
    idle.classList.toggle('hidden', !!f && !forceIdle)

    if (f) st.lastGood = f

    panes.classList.toggle('split', st.split)
    stalePane.root.classList.toggle('hidden', !st.split)

    // AUTO-FIT. The ladder must never overflow its box: a clipped best bid or
    // best ask is the one thing on this screen that must always be visible.
    // Depth is chosen by measurement rather than hardcoded, because the venue
    // projector's resolution is not known in advance and hero mode scales
    // everything by 1.3. Re-fitted only when the layout key changes, so this
    // costs one forced layout on resize, not one per frame.
    // The pane's own height is part of the key, not just the viewport: the
    // footer and the room grid grow as stats and sessions arrive, which shrinks
    // the pane after the first fit. Keying only on the viewport caches a depth
    // that was correct for a taller pane and never re-checks it.
    const key = `${innerWidth}x${innerHeight}|${document.body.classList.contains('hero')}|${st.split}|${panes.clientHeight}`
    // Measure the rendered span from the first child's top to the last child's
    // bottom, NOT scrollHeight: a centre-justified flex column overflows in both
    // directions, and overflow above the start edge is not scrollable, so
    // scrollHeight silently under-reports it and the fit loop stops too early.
    const over = (p) => {
      const ch = p.ladder.children
      if (!ch.length) return 0
      const span = ch[ch.length - 1].getBoundingClientRect().bottom - ch[0].getBoundingClientRect().top
      const cs = getComputedStyle(p.ladder)
      const avail = p.ladder.clientHeight - parseFloat(cs.paddingTop) - parseFloat(cs.paddingBottom)
      return span - avail
    }
    const fit = (levels) => {
      renderLadder(freshPane, f, null, scale, levels)
      if (st.split) renderLadder(stalePane, s, f, scale, levels)
      return Math.max(over(freshPane), st.split ? over(stalePane) : 0)
    }
    // Only cache the fit once there is actually a book to measure. Fitting an
    // empty ladder "succeeds" at max depth and then caches that answer forever,
    // so the first real frame renders at a depth that was never checked.
    if (f && key !== st.fitKey) {
      st.fitKey = key
      st.levels = MAX_LEVELS
      while (st.levels > MIN_LEVELS && fit(st.levels) > 0) st.levels--
    } else {
      fit(st.levels)
    }

    const lagFrames = f && s ? Math.max(0, f.seq - s.seq) : 0
    const lagMs = f && s ? Math.max(0, f.as_of_ms - s.as_of_ms) : 0
    freshPane.lag.innerHTML = ''
    freshPane.lag.append(el('span', null, f ? `seq ${num(f.seq)}` : ''))
    stalePane.lag.innerHTML = ''
    if (s) {
      stalePane.lag.append(el('span', null, `seq ${num(s.seq)} · behind by `))
      stalePane.lag.append(el('b', null, `${(lagMs / 1000).toFixed(1)}s`))
      stalePane.lag.append(el('span', null, ` / ${num(lagFrames)} frames`))
    }

    const bt = bannerText(lagMs, lagFrames, st.stats ? st.stats.chaos_dropped : 0)
    banner.textContent = bt || ''
    banner.classList.toggle('hidden', !bt)

    // Invariant strip — the proof that stays green while delivery rots.
    // A halt frame's report OUTRANKS the last good book's: the whole point of
    // halting is that the strip goes red, so it must not be masked by the
    // still-green report attached to the last frame that was correct.
    const iv = (st.halt && st.halt.inv) || (f && f.inv) || null
    inv.innerHTML = ''
    for (const [k, label] of CHECKS) {
      const ok = iv ? iv[k] !== false : true
      inv.append(el('div', `chk${ok ? '' : ' bad'}`, `${ok ? '✓' : '✗'} ${label}`))
    }

    // counters
    const t = st.stats
    count.innerHTML = ''
    if (t) {
      const b = el('b', null, num(t.orders))
      count.append(b, el('span', null, ' ORDERS'))
    }
    live.className = `live${st.live ? '' : ' off'}`
    liveTxt.textContent = st.live ? 'LIVE' : 'WAITING'

    stats.innerHTML = ''
    const stat = (v, label, cls) => {
      const d = el('div', `stat ${cls || ''}`)
      d.append(el('b', null, v), el('span', null, label))
      stats.append(d)
    }
    if (t) {
      stat(num(t.clients), 'phones', 'phones')
      stat(num(t.backpressure), 'dropped · slow phone', t.backpressure ? 'warn' : '')
      stat(num(t.chaos_dropped), 'dropped · chaos', t.chaos_dropped ? 'chaos' : '')
      stat(`${num(t.chaos_delay_ms)}ms`, 'injected delay', t.chaos_delay_ms ? 'chaos' : 'minor')
      stat(num(t.goroutines), 'goroutines', 'ok')
    }

    // presenter panel
    if (showGrid) renderGrid()

    // halt overlay
    if (st.halt) {
      halt.classList.remove('hidden')
      halt.innerHTML = ''
      const v = st.halt.violation || {}
      halt.append(el('h2', null, 'BOOK HALTED'))
      const what = el('div', 'what')
      what.append(document.createTextNode('invariant failed: '))
      what.append(el('em', null, v.kind || 'UNKNOWN'))
      halt.append(what)
      if (v.want != null) halt.append(el('div', 'meta', `${v.detail || ''} — want ${num(v.want)}, got ${num(v.got)}`))
      halt.append(el('div', 'meta', `seq ${num(st.halt.seq)} · engine quarantined · no further mutation accepted`))
      halt.append(el('div', 'lkg', 'last known good book shown below'))
    } else {
      halt.classList.add('hidden')
    }
  }

  function renderGrid() {
    // The room populates ITSELF from who has actually traded, rather than from a
    // roster handed down at connect time. The live server does not know who is
    // in the room — nobody logs in — and a grid that stays empty until someone
    // supplies a list is a grid that is empty on the day. A cell appears the
    // first time a session acts, which is also the moment it earns one.
    const seen = new Set([...(st.meta.roster || []), ...st.sessions.keys()])
    const roster = [...seen].sort().slice(0, MAX_CELLS)
    for (const id of roster) {
      let cell = gridWrap.querySelector(`[data-s="${id}"]`)
      if (!cell) {
        cell = el('div', 'cell', id)
        cell.dataset.s = id
        gridWrap.append(cell)
      }
      const seen = st.sessions.get(id)
      const age = seen == null ? Infinity : st.clock - seen
      cell.classList.toggle('warm', age < 4000)
    }
    // Drop cells for sessions no longer in the window, so the grid cannot grow
    // without bound over a long session.
    const keep = new Set(roster)
    gridWrap.querySelectorAll('.cell').forEach((c) => {
      if (!keep.has(c.dataset.s)) c.remove()
    })
  }

  function pulse(id) {
    const cell = gridWrap.querySelector(`[data-s="${id}"]`)
    if (!cell) return
    cell.classList.add('hot')
    clearTimeout(hotTimers.get(id))
    hotTimers.set(id, setTimeout(() => cell.classList.remove('hot'), 220))
  }

  function schedule() {
    if (frozen || dirty) return
    dirty = true
    requestAnimationFrame(render)
  }

  // ── inbound ─────────────────────────────────────────────────────────────────
  function applyFrame(f) {
    st.live = true
    if (f.as_of_ms != null) st.clock = Math.max(st.clock, f.as_of_ms)

    if (f.type === 'book') {
      const pane = f.pane || 'fresh'
      const cur = st.panes[pane]
      // Reorder guard, and it is load-bearing: when chaos is switched off,
      // frames still sitting in the delay ring arrive AFTER newly passed-through
      // ones. Without this the degraded pane visibly jumps backwards on stage.
      if (cur && f.seq <= cur.seq) return
      st.panes[pane] = f
      if (pane === 'fresh' && f.actor) {
        st.sessions.set(f.actor, f.as_of_ms)
        if (!frozen) pulse(f.actor)   // the pulse is render, so freeze holds it too
      }
    } else if (f.type === 'stats') {
      st.stats = f
      if (f.split != null) st.split = f.split
    } else if (f.type === 'halt') {
      st.halt = f
    }
    schedule()
  }

  function applyMeta(m) {
    st.meta = { ...st.meta, ...m }
    if (m.connected != null) st.live = m.connected
    if (m.split_arms_at_ms === null) st.split = false
    if (m.roster) { gridWrap.querySelectorAll('.cell').forEach((c) => c.remove()) }
    if (m.title) h1.textContent = m.title
    if (m.subtitle) sub.textContent = m.subtitle
    const url = m.lan_url || m.url
    if (url) renderQR(url)
    // A tape restart must not leave the previous run's book on screen.
    if (m.frames || m.roster) { st.panes.fresh = null; st.panes.stale = null; st.halt = null; st.split = false }
    schedule()
  }

  // ── freeze ──────────────────────────────────────────────────────────────────
  // Rendering only. The socket keeps reading, the engine keeps matching, seq
  // keeps advancing. Unfreeze and the book has moved on — which is the proof.
  addEventListener('keydown', (e) => {
    if (e.code === 'Space') {
      e.preventDefault()
      frozen = !frozen
      document.body.classList.toggle('frozen', frozen)
      if (!frozen) schedule()
    }
    if (e.key === 'h' || e.key === 'H') {
      document.body.classList.toggle('hero')
      const on = document.body.classList.contains('hero')
      document.documentElement.style.setProperty('--s', on ? '1.3' : '1')
      st.fitKey = null   // +30% type needs the ladder depth re-fitted
      if (!frozen) schedule()
    }
  })

  render()
  return { applyFrame, applyMeta }
}
