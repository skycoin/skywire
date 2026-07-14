import { useEffect, useState, useCallback } from 'react'
import { api } from '../api'

function Market() {
  const [showCreateListing, setShowCreateListing] = useState(false)
  const [products, setProducts] = useState([])
  const [currencies, setCurrencies] = useState([])
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const [busy, setBusy] = useState(false)

  const [newListing, setNewListing] = useState({
    amount_sky: '',
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

  // Load currencies once, products on an interval (simple polling).
  useEffect(() => {
    api
      .getCurrencies()
      .then((d) => {
        const list = d.currencies || []
        setCurrencies(list)
        setNewListing((prev) => ({ ...prev, payment_currency: prev.payment_currency || list[0] || '' }))
      })
      .catch(() => {})
    loadProducts()
    const id = setInterval(loadProducts, 5000)
    return () => clearInterval(id)
  }, [loadProducts])

  const flash = (msg) => {
    setNotice(msg)
    setTimeout(() => setNotice(''), 8000)
  }

  const handleBuy = async (product) => {
    setError('')
    setBusy(true)
    try {
      const order = await api.buyProduct(product.id)
      flash(
        `Order placed. Pay exactly ${order.expected_payment_amount} ${order.payment_currency} to ${order.seller_wallet} before ${new Date(order.expires_at).toLocaleTimeString()}.`
      )
      loadProducts()
    } catch (e) {
      setError(e.message)
    } finally {
      setBusy(false)
    }
  }

  const handleCreateListing = async (e) => {
    e.preventDefault()
    setError('')
    setBusy(true)
    try {
      const resp = await api.createListing({
        amount_sky: parseFloat(newListing.amount_sky),
        price: parseFloat(newListing.price),
        payment_currency: newListing.payment_currency,
      })
      flash(
        `Listing created. Transfer exactly ${resp.expected_amount_sky} SKY ` +
          `(${resp.amount_sky} amount + ${resp.commission_sky} commission) ` +
          `to the market wallet ${resp.market_wallet} within 15 minutes.`
      )
      setShowCreateListing(false)
      setNewListing({ amount_sky: '', price: '', payment_currency: currencies[0] || '' })
    } catch (err) {
      setError(err.message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <div>
      <div className="d-flex justify-content-between align-items-center mb-4">
        <h2>Market</h2>
        <button
          className="btn btn-connect"
          onClick={() => setShowCreateListing(!showCreateListing)}
        >
          {showCreateListing ? 'Cancel' : '+ Create Sell Order'}
        </button>
      </div>

      {notice && <div className="alert alert-info">{notice}</div>}
      {error && <div className="alert alert-danger">{error}</div>}

      {showCreateListing && (
        <div className="card mb-4">
          <h4>Create New Sell Order</h4>
          <form onSubmit={handleCreateListing}>
            <div className="row mb-3">
              <div className="col-md-4">
                <label className="form-label">SKY Amount</label>
                <input
                  type="number"
                  className="form-control"
                  placeholder="e.g. 10"
                  value={newListing.amount_sky}
                  onChange={(e) => setNewListing({ ...newListing, amount_sky: e.target.value })}
                  required
                  step="0.0001"
                  min="0"
                />
              </div>
              <div className="col-md-4">
                <label className="form-label">Price</label>
                <input
                  type="number"
                  className="form-control"
                  placeholder="e.g. 2.00"
                  value={newListing.price}
                  onChange={(e) => setNewListing({ ...newListing, price: e.target.value })}
                  required
                  step="0.0001"
                  min="0"
                />
              </div>
              <div className="col-md-4">
                <label className="form-label">Payment Currency</label>
                <select
                  className="form-select"
                  value={newListing.payment_currency}
                  onChange={(e) => setNewListing({ ...newListing, payment_currency: e.target.value })}
                  required
                >
                  {currencies.length === 0 && <option value="">No currencies enabled</option>}
                  {currencies.map((c) => (
                    <option key={c} value={c}>{c}</option>
                  ))}
                </select>
              </div>
            </div>
            <button type="submit" className="btn btn-connect" disabled={busy || currencies.length === 0}>
              Create Sell Order
            </button>
          </form>
          <small className="text-muted mt-2 d-block">
            After creating the order, you have 15 minutes to transfer SKY to the market wallet.
          </small>
        </div>
      )}

      <h4 className="mb-3">Available Products</h4>
      {products.length === 0 ? (
        <div className="alert alert-secondary">No products available for sale.</div>
      ) : (
        <div className="row">
          {products.map((product) => (
            <div key={product.id} className="col-md-6 col-lg-4 mb-3">
              <div className="card product-card">
                <div className="product-amount mb-2">
                  <strong>{product.amount_sky} SKY</strong>
                </div>
                <div className="product-price mb-2">
                  {product.price} {product.payment_currency}
                </div>
                <div className="text-muted small mb-3">
                  Seller: <code>{shorten(product.seller_pubkey)}</code>
                </div>
                <button className="btn btn-connect w-100" disabled={busy} onClick={() => handleBuy(product)}>
                  Buy
                </button>
              </div>
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
