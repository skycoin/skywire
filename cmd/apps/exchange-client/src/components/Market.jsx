import { useEffect, useState, useCallback } from 'react'
import { api } from '../api'

function Market({ registered, marketReady, sellCoins = [], currencies = [], onNeedRegister }) {
  const [showCreateListing, setShowCreateListing] = useState(false)
  const [products, setProducts] = useState([])
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const [busy, setBusy] = useState(false)

  const [newListing, setNewListing] = useState({
    sell_coin: '',
    amount: '',
    price: '',
    payment_currency: '',
  })

  const loadProducts = useCallback(async () => {
    try {
      const data = await api.getProducts()
      setProducts(data.products || [])
      setError('')
    } catch (e) {
      setError(e.message)
    }
  }, [])

  useEffect(() => {
    loadProducts()
    const id = setInterval(loadProducts, 5000)
    return () => clearInterval(id)
  }, [loadProducts])

  // Seed sensible defaults for the create form once the coin lists arrive.
  useEffect(() => {
    setNewListing((prev) => {
      const sell = prev.sell_coin || sellCoins[0] || ''
      const payOpts = [...new Set([...currencies, ...sellCoins.filter((c) => c !== sell)])]
      return {
        ...prev,
        sell_coin: sell,
        payment_currency: prev.payment_currency || payOpts[0] || '',
      }
    })
  }, [sellCoins, currencies])

  const flash = (msg) => {
    setNotice(msg)
    setTimeout(() => setNotice(''), 9000)
  }

  // Surface the market's "register first" as an actionable prompt, not a dead end.
  const handleErr = (e) => {
    if (/register/i.test(e.message) && onNeedRegister) onNeedRegister()
    setError(e.message)
  }

  const handleBuy = async (product) => {
    if (!registered) return onNeedRegister && onNeedRegister()
    setError('')
    setBusy(true)
    try {
      const order = await api.buyProduct(product.id)
      flash(
        `Order placed for ${order.amount} ${order.sell_coin}. Pay exactly ${order.expected_payment_amount} ` +
          `${order.payment_currency} to ${order.seller_wallet} before ${new Date(order.expires_at).toLocaleTimeString()}.`
      )
      loadProducts()
    } catch (e) {
      handleErr(e)
    } finally {
      setBusy(false)
    }
  }

  const openCreate = () => {
    if (!registered) return onNeedRegister && onNeedRegister()
    setShowCreateListing((v) => !v)
  }

  const handleCreateListing = async (e) => {
    e.preventDefault()
    setError('')
    setBusy(true)
    try {
      const resp = await api.createListing({
        sell_coin: newListing.sell_coin,
        amount: parseFloat(newListing.amount),
        price: parseFloat(newListing.price),
        payment_currency: newListing.payment_currency,
      })
      flash(
        `Listing created. Transfer exactly ${resp.expected_amount} ${resp.sell_coin} ` +
          `(${resp.amount} amount + ${resp.commission} commission) to ${resp.market_wallet} within 15 minutes.`
      )
      setShowCreateListing(false)
      setNewListing({ sell_coin: sellCoins[0] || '', amount: '', price: '', payment_currency: '' })
    } catch (err) {
      handleErr(err)
    } finally {
      setBusy(false)
    }
  }

  // Payment options = external explorer coins plus any other fibercoin (you can't
  // pay in the coin you're selling).
  const paymentOptions = [...new Set([...currencies, ...sellCoins.filter((c) => c !== newListing.sell_coin)])]
  const canSell = sellCoins.length > 0

  return (
    <div>
      <div className="page-head">
        <h2>Market</h2>
        <button className="btn btn-connect" onClick={openCreate} disabled={!canSell}>
          {showCreateListing ? 'Cancel' : '+ New Sell Order'}
        </button>
      </div>

      {notice && <div className="alert alert-info">{notice}</div>}
      {error && <div className="alert alert-danger">{error}</div>}

      {showCreateListing && (
        <div className="panel trade-builder mb-4">
          <h4 className="panel-title">New sell order</h4>
          <form onSubmit={handleCreateListing}>
            {/* "You sell" leg — the coin + amount you give up. */}
            <div className="trade-leg">
              <span className="leg-label">You sell</span>
              <input
                type="number"
                className="form-control leg-amount"
                placeholder="0.00"
                value={newListing.amount}
                onChange={(e) => setNewListing({ ...newListing, amount: e.target.value })}
                required
                step="0.0001"
                min="0"
                aria-label="amount to sell"
              />
              <select
                className="form-select leg-coin"
                value={newListing.sell_coin}
                onChange={(e) => {
                  const sell = e.target.value
                  const pay = newListing.payment_currency === sell ? '' : newListing.payment_currency
                  setNewListing({ ...newListing, sell_coin: sell, payment_currency: pay })
                }}
                required
                aria-label="sell coin"
              >
                {sellCoins.length === 0 && <option value="">No sell coins</option>}
                {sellCoins.map((c) => (
                  <option key={c} value={c}>{c}</option>
                ))}
              </select>
            </div>

            <div className="trade-arrow">↓</div>

            {/* "You get" leg — price sits next to the currency it's denominated in. */}
            <div className="trade-leg">
              <span className="leg-label">You get</span>
              <input
                type="number"
                className="form-control leg-amount"
                placeholder="0.00"
                value={newListing.price}
                onChange={(e) => setNewListing({ ...newListing, price: e.target.value })}
                required
                step="0.0001"
                min="0"
                aria-label="price"
              />
              <select
                className="form-select leg-coin"
                value={newListing.payment_currency}
                onChange={(e) => setNewListing({ ...newListing, payment_currency: e.target.value })}
                required
                aria-label="payment currency"
              >
                {paymentOptions.length === 0 && <option value="">No currencies</option>}
                {paymentOptions.map((c) => (
                  <option key={c} value={c}>{c}</option>
                ))}
              </select>
            </div>

            <div className="trade-summary">
              {newListing.amount && newListing.price && newListing.sell_coin && newListing.payment_currency ? (
                <>
                  Sell <strong>{newListing.amount} {newListing.sell_coin}</strong> for{' '}
                  <strong>{newListing.price} {newListing.payment_currency}</strong>. After creating this
                  order you have 15 minutes to deposit the {newListing.sell_coin} (amount + a small
                  commission) to the market escrow; the buyer’s payment goes straight to your wallet.
                </>
              ) : (
                <>Set what you sell and what you want for it. Payment can be an external coin (BTC/LTC) or another fibercoin.</>
              )}
            </div>

            <button
              type="submit"
              className="btn btn-connect"
              disabled={busy || paymentOptions.length === 0 || sellCoins.length === 0}
            >
              Create sell order
            </button>
          </form>
        </div>
      )}

      <h4 className="section-title">Available products</h4>
      {products.length === 0 ? (
        <div className="alert alert-secondary">
          {marketReady === false
            ? 'This market has no coins configured yet.'
            : 'No products available right now.'}
        </div>
      ) : (
        <div className="card-grid">
          {products.map((product) => (
            <div key={product.id} className="card product-card">
              <div className="product-amount">
                <strong>{product.amount} {product.sell_coin}</strong>
              </div>
              <div className="product-price">
                {product.price} {product.payment_currency}
              </div>
              <div className="text-muted small product-seller">
                Seller <code>{shorten(product.seller_pubkey)}</code>
              </div>
              <button className="btn btn-connect w-100" disabled={busy} onClick={() => handleBuy(product)}>
                Buy
              </button>
            </div>
          ))}
        </div>
      )}
    </div>
  )
}

function shorten(pk) {
  if (!pk || pk.length <= 14) return pk
  return `${pk.slice(0, 8)}…${pk.slice(-4)}`
}

export default Market
