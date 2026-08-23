// trader.js — the phone view.
//
// The design constraint that drives everything: THE ROOM'S PARTICIPATION RATE IS
// THE DEMO. A book with four orders in it photographs badly, produces no trades,
// and gives the invariant checker nothing to check. So the fastest path from
// "I want to buy" to "I have bought" is four taps at most, and none of them is
// on a keyboard.
//
// Prices come from presets anchored to the live best bid and ask; sizes come
// from chips. Free entry exists underneath for anyone who wants it, which is
// roughly nobody once the buttons are there.
//
// Everything below assumes a bad connection: submissions are optimistic and
// shown immediately as pending, the socket reconnects with backoff, and the
// session id survives a reload so a phone that drops does not become a stranger.

const SIZES = [50, 100, 250, 500]

const el = (tag, cls, txt) => {
  const n = document.createElement(tag)
  if (cls) n.className = cls
  if (txt != null) n.textContent = txt
  return n
}

// A session id, not a login. Persisted so a reload or a dropped connection
// rejoins as the same trader and keeps its fills.
function sessionID() {
  const KEY = 'oo.session'
  let s = null
  try { s = sessionStorage.getItem(KEY) } catch { /* private mode */ }
  if (!s) {
    s = Array.from({ length: 3 }, () =>
      'abcdefghjkmnpqrstuvwxyz23456789'[Math.floor(Math.random() * 30)]).join('')
    try { sessionStorage.setItem(KEY, s) } catch { /* ignore */ }
  }
  return s
}

const buzz = (ms) => { try { navigator.vibrate?.(ms) } catch { /* unsupported */ } }

export function mountTrader(root, { qs, socketSource, fixture } = {}) {
  const session = sessionID()
  const money = (c) => (c / (st.scale || 100)).toFixed(2)

  const st = {
    scale: 100,
    bid: null,     // {px, qty}
    ask: null,
    last: null,
    size: 100,
    fills: [],     // newest first; {side, px, qty, at, pending, rejected}
    connected: false,
    seq: 0,
  }
  let send = () => {}

  // ── DOM ───────────────────────────────────────────────────────────────────
  root.innerHTML = ''
  const wrap = el('div', 'trader')

  const head = el('div', 't-head')
  const you = el('div', 't-you')
  const conn = el('div', 't-conn dead')
  you.append(conn, el('span', null, session))
  head.append(el('h1', null, 'OPEN OUTCRY'), you)

  const market = el('div', 't-market')
  const bidBox = el('div', 't-px bid')
  const bidVal = el('div', 'val', '—'); const bidSz = el('div', 'sz', '')
  bidBox.append(el('div', 'lbl', 'BID'), bidVal, bidSz)
  const spread = el('div', 't-spread')
  const spreadVal = el('b', null, '—')
  spread.append(spreadVal, el('span', null, 'SPREAD'))
  const askBox = el('div', 't-px ask')
  const askVal = el('div', 'val', '—'); const askSz = el('div', 'sz', '')
  askBox.append(el('div', 'lbl', 'ASK'), askVal, askSz)
  market.append(bidBox, spread, askBox)

  const fills = el('div', 't-fills')

  const sizeRow = el('div', 't-size')
  sizeRow.append(el('div', 'lbl', 'SIZE'))
  const chips = SIZES.map((n) => {
    const c = el('button', 'chip' + (n === st.size ? ' on' : ''), String(n))
    c.addEventListener('click', () => { st.size = n; buzz(8); render() })
    sizeRow.append(c)
    return { n, node: c }
  })

  // Four actions. Left column buys, right column sells; top row is passive,
  // bottom row crosses. Position carries the meaning as much as colour does.
  const actions = el('div', 't-actions')
  const mkAct = (cls, verb, hint) => {
    const b = el('button', `act ${cls}`)
    const px = el('div', 'px', '—')
    b.append(el('div', 'verb', verb), px, el('div', 'hint', hint))
    actions.append(b)
    return { node: b, px }
  }
  const aBid = mkAct('buy', 'BID', 'join the queue')
  const aOffer = mkAct('sell', 'OFFER', 'join the queue')
  const aLift = mkAct('buy hot', 'LIFT', 'buy now')
  const aHit = mkAct('sell hot', 'HIT', 'sell now')

  const free = el('details', 't-free')
  free.append(el('summary', null, 'enter a price yourself'))
  const frow = el('div', 'row')
  const pxWrap = el('div'); const pxLbl = el('label', null, 'PRICE')
  const pxIn = el('input'); pxIn.type = 'text'; pxIn.inputMode = 'decimal'; pxIn.placeholder = '102.40'
  pxWrap.append(pxLbl, pxIn)
  const qtyWrap = el('div'); const qtyLbl = el('label', null, 'QUANTITY')
  const qtyIn = el('input'); qtyIn.type = 'text'; qtyIn.inputMode = 'numeric'; qtyIn.placeholder = '100'
  qtyWrap.append(qtyLbl, qtyIn)
  const sendRow = el('div', 'send')
  const freeBuy = el('button', 'b', 'BUY'); const freeSell = el('button', 's', 'SELL')
  sendRow.append(freeBuy, freeSell)
  frow.append(pxWrap, qtyWrap, sendRow)
  free.append(frow)

  wrap.append(head, market, fills, sizeRow, actions, free)
  root.append(wrap)

  const toast = el('div', 't-toast')
  root.append(toast)
  let toastTimer = null
  function say(msg, bad) {
    toast.textContent = msg
    toast.className = 't-toast show' + (bad ? ' bad' : '')
    clearTimeout(toastTimer)
    toastTimer = setTimeout(() => { toast.className = 't-toast' + (bad ? ' bad' : '') }, 1500)
  }

  // ── submitting ────────────────────────────────────────────────────────────
  function submit(side, priceCents, qty, label) {
    if (!Number.isFinite(priceCents) || priceCents <= 0 || !Number.isFinite(qty) || qty <= 0) {
      say('need a price and a size', true)
      return
    }
    // Optimistic: the row appears immediately and is reconciled when the server
    // answers. On a bad connection this is the difference between a UI that
    // feels broken and one that feels slow.
    const pending = { side, px: priceCents, qty, at: Date.now(), pending: true }
    st.fills.unshift(pending)
    buzz(14)
    say(`${label} ${qty} @ ${money(priceCents)}`)
    send({ type: 'submit', side, price: priceCents, qty })
    render()
  }

  aBid.node.addEventListener('click', () => st.bid && submit('buy', st.bid.px, st.size, 'bid'))
  aOffer.node.addEventListener('click', () => st.ask && submit('sell', st.ask.px, st.size, 'offer'))
  aLift.node.addEventListener('click', () => st.ask && submit('buy', st.ask.px, st.size, 'lift'))
  aHit.node.addEventListener('click', () => st.bid && submit('sell', st.bid.px, st.size, 'hit'))

  const freePrice = () => Math.round(parseFloat(pxIn.value) * (st.scale || 100))
  const freeQty = () => parseInt(qtyIn.value || String(st.size), 10)
  freeBuy.addEventListener('click', () => submit('buy', freePrice(), freeQty(), 'buy'))
  freeSell.addEventListener('click', () => submit('sell', freePrice(), freeQty(), 'sell'))

  // ── render ────────────────────────────────────────────────────────────────
  function render() {
    conn.className = 't-conn' + (st.connected ? '' : ' off')

    bidVal.textContent = st.bid ? money(st.bid.px) : '—'
    bidSz.textContent = st.bid ? `${st.bid.qty.toLocaleString()} shares` : ''
    askVal.textContent = st.ask ? money(st.ask.px) : '—'
    askSz.textContent = st.ask ? `${st.ask.qty.toLocaleString()} shares` : ''
    spreadVal.textContent = st.bid && st.ask ? money(st.ask.px - st.bid.px) : '—'

    aBid.px.textContent = st.bid ? money(st.bid.px) : '—'
    aOffer.px.textContent = st.ask ? money(st.ask.px) : '—'
    aLift.px.textContent = st.ask ? money(st.ask.px) : '—'
    aHit.px.textContent = st.bid ? money(st.bid.px) : '—'
    aBid.node.disabled = !st.bid
    aHit.node.disabled = !st.bid
    aOffer.node.disabled = !st.ask
    aLift.node.disabled = !st.ask

    for (const c of chips) c.node.className = 'chip' + (c.n === st.size ? ' on' : '')

    fills.innerHTML = ''
    if (!st.fills.length) {
      fills.append(el('div', 'empty', 'Your fills appear here.\nPick a size, then tap a price.'))
    } else {
      for (const f of st.fills.slice(0, 40)) {
        const row = el('div', `t-fill ${f.side}${f.pending ? ' pending' : ''}${f.rejected ? ' rejected' : ''}`)
        row.append(
          el('div', 'side', f.rejected ? 'REJ' : f.side.toUpperCase()),
          el('div', 'qty', `${f.qty.toLocaleString()} @ ${money(f.px)}`),
          el('div', 'when', f.rejected ? String(f.rejected) : new Date(f.at)
            .toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' })),
        )
        fills.append(row)
      }
    }
  }

  // ── inbound ───────────────────────────────────────────────────────────────
  function applyFrame(f) {
    if (!f || typeof f !== 'object') return
    if (f.px_scale) st.scale = f.px_scale

    switch (f.type) {
      case 'top':
      case 'book': {
        // Accept either the trader's lightweight top-of-book frame or a full
        // book frame, so the fixture tape drives this view unchanged.
        st.seq = f.seq ?? st.seq
        if (f.bid || f.ask) {
          st.bid = f.bid || null
          st.ask = f.ask || null
        } else if (f.bids || f.asks) {
          const b = (f.bids || [])[0]
          const a = (f.asks || [])[0]
          st.bid = b ? { px: b[0], qty: b[1] } : null
          st.ask = a ? { px: a[0], qty: a[1] } : null
        }
        if (f.last) st.last = f.last
        break
      }
      case 'fill': {
        // Reconcile the optimistic row rather than appending a duplicate.
        const p = st.fills.find((x) => x.pending && x.side === f.side && x.px === f.px)
        if (p) { p.pending = false; p.qty = f.qty; p.at = Date.now() }
        else st.fills.unshift({ side: f.side, px: f.px, qty: f.qty, at: Date.now() })
        buzz([10, 40, 10])
        say(`filled ${f.qty} @ ${money(f.px)}`)
        break
      }
      case 'rested': {
        const p = st.fills.find((x) => x.pending && x.side === f.side && x.px === f.px)
        if (p) { p.pending = false; p.resting = true }
        break
      }
      case 'reject': {
        const p = st.fills.find((x) => x.pending)
        if (p) { p.pending = false; p.rejected = f.reason || 'rejected' }
        say(f.reason || 'rejected', true)
        break
      }
      case 'hello': {
        if (f.px_scale) st.scale = f.px_scale
        break
      }
    }
    render()
  }

  function applyMeta(m) {
    if (m && m.connected != null) st.connected = m.connected
    if (m && m.px_scale) st.scale = m.px_scale
    render()
  }

  // ── source ────────────────────────────────────────────────────────────────
  if (fixture) {
    // Same discipline as the projector: the view is finished and demonstrable
    // before any server exists. Taps are echoed back locally so the full
    // interaction — pending, then filled — can be exercised offline.
    st.connected = true
    send = (cmd) => {
      setTimeout(() => {
        const crosses = cmd.side === 'buy'
          ? (st.ask && cmd.price >= st.ask.px)
          : (st.bid && cmd.price <= st.bid.px)
        applyFrame(crosses
          ? { type: 'fill', side: cmd.side, px: cmd.price, qty: cmd.qty }
          : { type: 'rested', side: cmd.side, px: cmd.price, qty: cmd.qty })
      }, 180)
    }
    fixtureTop(applyFrame, applyMeta)
  } else {
    const sock = socketSource('trader', applyFrame, applyMeta, session)
    send = (cmd) => sock.send(cmd)
  }

  render()
  return { applyFrame, applyMeta }
}

// Replays the committed tape's fresh-lane book frames to drive top of book.
async function fixtureTop(onFrame, onMeta) {
  const tape = await (await fetch('/fixtures/tape.json')).json()
  onMeta({ connected: true, px_scale: 100 })
  const frames = tape.frames
    .filter((f) => f.pane === 'fresh' && f.type === 'book')
    .sort((a, b) => a.at_ms - b.at_ms)
  const t0 = performance.now()
  let i = 0
  const pump = () => {
    const now = performance.now() - t0
    while (i < frames.length && frames[i].at_ms <= now) onFrame(frames[i++])
    if (i < frames.length) requestAnimationFrame(pump)
    else { i = 0; setTimeout(() => requestAnimationFrame(pump), 1200) }
  }
  requestAnimationFrame(pump)
}
