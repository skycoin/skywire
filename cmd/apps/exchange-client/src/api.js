// Thin wrapper around the exchange-client local control API. Every call returns
// the parsed JSON body; on a non-2xx response it throws an Error carrying the
// server's message so callers can surface it.

async function request(method, url, body) {
  const opts = { method, headers: {} }
  if (body !== undefined) {
    opts.headers['Content-Type'] = 'application/json'
    opts.body = JSON.stringify(body)
  }
  const res = await fetch(url, opts)
  let data = {}
  try {
    data = await res.json()
  } catch {
    // empty/invalid body
  }
  if (!res.ok) {
    throw new Error(data.error || `request failed (${res.status})`)
  }
  return data
}

// Wallet addresses are stored locally as a convenience so the Settings form is
// pre-filled and the app can prompt first-time users to register. The market
// remains the source of truth; this is only a UX hint.
const WALLETS_KEY = 'exchange:wallets'

export function loadWallets() {
  try {
    return JSON.parse(localStorage.getItem(WALLETS_KEY)) || {}
  } catch {
    return {}
  }
}

export function saveWallets(w) {
  try {
    localStorage.setItem(WALLETS_KEY, JSON.stringify(w))
  } catch {
    // ignore storage errors (private mode, etc.)
  }
}

export function hasSavedWallets() {
  const w = loadWallets()
  return !!(w && w.SKY)
}

// Copy text to the clipboard, with a fallback for non-secure (plain http)
// contexts where navigator.clipboard is unavailable — the app is served over
// http on the visor, so the modern API is often missing. Returns true on
// success. Callers can surface their own "copied" feedback.
export async function copyToClipboard(text) {
  if (!text) return false
  try {
    if (navigator.clipboard && window.isSecureContext) {
      await navigator.clipboard.writeText(text)
      return true
    }
  } catch {
    // fall through to the legacy execCommand path
  }
  try {
    const ta = document.createElement('textarea')
    ta.value = text
    ta.style.position = 'fixed'
    ta.style.top = '-1000px'
    ta.style.opacity = '0'
    document.body.appendChild(ta)
    ta.focus()
    ta.select()
    const ok = document.execCommand('copy')
    document.body.removeChild(ta)
    return ok
  } catch {
    return false
  }
}

export const api = {
  getConfig: () => request('GET', '/api/config'),
  getStatus: () => request('GET', '/api/status'),
  connect: (marketPK) => request('POST', '/api/connect', { market_pk: marketPK }),
  disconnect: () => request('POST', '/api/disconnect'),

  register: (wallets) => request('POST', '/api/register', wallets),
  getCurrencies: () => request('GET', '/api/currencies'),
  getProducts: () => request('GET', '/api/products'),
  getOrders: () => request('GET', '/api/orders'),
  getListings: () => request('GET', '/api/listings/mine'),
  createListing: (listing) => request('POST', '/api/listings', listing),
  cancelListing: (listingId) => request('POST', '/api/listings/cancel', { listing_id: listingId }),
  cancelOrder: (orderId) => request('POST', '/api/orders/cancel', { order_id: orderId }),
  buyProduct: (productId) => request('POST', '/api/buy', { product_id: productId }),
  getOrderStatus: (orderId) => request('POST', '/api/order-status', { order_id: orderId }),
}
