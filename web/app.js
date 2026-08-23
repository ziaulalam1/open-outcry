// app.js — parse the URL, pick a frame source, mount a view. That is all it does.
//
// The load-bearing property of this file: the fixture source and the live socket
// source both call the SAME applyFrame() on the SAME view object. The projector
// cannot accidentally depend on something only the live engine provides, because
// for its entire construction the live engine did not exist.

import { mountProjector } from './projector.js'
import { mountTrader } from './trader.js'

const qs = new URLSearchParams(location.search)
const view = qs.get('view') || 'projector'
const hero = qs.get('hero') === 'true' || qs.get('hero') === '1'
const fixture = qs.get('fixture')
const speed = Number(qs.get('speed') || 1)
const root = document.getElementById('root')

// ── Frame sources ─────────────────────────────────────────────────────────────

// Replays a recorded tape against a synthetic clock. No WebSocket is opened.
// This is what makes "the view is the spec" structural rather than aspirational,
// and it doubles as the venue-outage fallback: if the wifi dies, the photograph
// is still obtainable.
async function fixtureSource(name, onFrame, onMeta) {
  const file = name === 'halt' ? '/fixtures/tape-halt.json' : '/fixtures/tape.json'
  const tape = await (await fetch(file)).json()
  onMeta(tape)

  const frames = tape.frames.slice().sort((a, b) => a.at_ms - b.at_ms)
  const t0 = performance.now()
  let i = 0
  const pump = () => {
    const now = (performance.now() - t0) * speed
    while (i < frames.length && frames[i].at_ms <= now) onFrame(frames[i++])
    if (i < frames.length) requestAnimationFrame(pump)
    else if (tape.duration_ms) {          // loop, so an unattended projector never
      i = 0; setTimeout(() => {           // goes blank between rehearsals
        onMeta(tape); requestAnimationFrame(pump)
      }, 1500)
    }
  }
  requestAnimationFrame(pump)
}

// The live path. Deliberately the thinner of the two: everything interesting
// already happened in the view, which was finished before this existed.
//
// Assumes the connection is bad, because in a room of thirty phones on one
// access point it will be. Reconnects with backoff, and buffers outbound orders
// across a drop so a tap during a two-second gap is still a trade rather than a
// silent no-op. The buffer is small and drops oldest-first: a stale order is
// worse than no order, and unbounded buffering on a phone is how you end up
// submitting forty orders at once when the wifi returns.
function socketSource(feed, onFrame, onMeta, session) {
  const MAX_QUEUED = 8
  let ws = null
  let backoff = 250
  let queued = []

  const flush = () => {
    if (!ws || ws.readyState !== WebSocket.OPEN) return
    const pending = queued
    queued = []
    for (const m of pending) ws.send(JSON.stringify(m))
  }

  const connect = () => {
    const proto = location.protocol === 'https:' ? 'wss' : 'ws'
    const q = new URLSearchParams({ feed })
    if (session) q.set('session', session)
    ws = new WebSocket(`${proto}://${location.host}/ws?${q}`)

    ws.onopen = () => { backoff = 250; onMeta({ connected: true }); flush() }
    ws.onmessage = (e) => {
      let f
      try { f = JSON.parse(e.data) } catch { return } // never let one bad frame kill the feed
      if (f.type === 'hello') onMeta({ ...f, connected: true })
      onFrame(f)
    }
    ws.onclose = () => {
      onMeta({ connected: false })
      setTimeout(connect, backoff)
      backoff = Math.min(backoff * 2, 5000) // a venue drops wifi; reconnect quietly
    }
    ws.onerror = () => ws.close()
  }
  connect()

  return {
    send(msg) {
      if (ws && ws.readyState === WebSocket.OPEN) { ws.send(JSON.stringify(msg)); return }
      queued.push(msg)
      if (queued.length > MAX_QUEUED) queued.shift()
    },
  }
}

// ── Mount ─────────────────────────────────────────────────────────────────────

if (view === 'trader') {
  mountTrader(root, { qs, socketSource, fixture })
} else {
  const p = mountProjector(root, {
    hero,
    showGrid: qs.get('grid') !== '0',
    idle: qs.get('idle') === '1',
  })
  if (fixture) {
    fixtureSource(fixture === '1' ? 'main' : fixture, p.applyFrame, p.applyMeta)
  } else {
    // Two sockets, not one multiplexed: a single connection cannot have two
    // writers, and multiplexing would head-of-line-block the fresh pane behind
    // the stale pane's delay ring — degrading the control exactly as much as
    // the experiment, which would destroy the comparison.
    socketSource('fresh', p.applyFrame, p.applyMeta)
    socketSource('stale', p.applyFrame, p.applyMeta)
  }
}
