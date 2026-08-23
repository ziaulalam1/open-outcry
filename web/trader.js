// trader.js — the phone view.
//
// CHECKPOINT 3 will build this against the approved spec: tap-to-bid and
// tap-to-lift preset buttons anchored to the current best bid/ask, with free
// entry underneath. Typing a price is friction, and participation rate is what
// decides whether the book is deep enough to photograph.
//
// It is a stub today on purpose: the projector view is the spec and gets
// finished first. Shipping a half-wired trader now would mean two unfinished
// surfaces instead of one finished one.

export function mountTrader(root) {
  root.innerHTML = ''
  const d = document.createElement('div')
  d.className = 'trader-stub'
  d.innerHTML = `
    <h1>OPEN OUTCRY</h1>
    <p>Trader view lands at checkpoint 3.</p>
    <p class="dim">Preset tap-to-bid / tap-to-lift, anchored to best bid &amp; ask.</p>
  `
  root.append(d)
}
