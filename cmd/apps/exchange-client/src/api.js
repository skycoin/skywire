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
  buyProduct: (productId) => request('POST', '/api/buy', { product_id: productId }),
  getOrderStatus: (orderId) => request('POST', '/api/order-status', { order_id: orderId }),
}
