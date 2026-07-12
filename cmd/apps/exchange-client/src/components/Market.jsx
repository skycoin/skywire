import { useState } from 'react'

function Market({ isConnected }) {
  const [showCreateListing, setShowCreateListing] = useState(false)
  
  // Mock data - will be replaced with real API calls
  const [products] = useState([
    { id: 1, amount: 10, price: 2.0, currency: 'USDT', seller: '02abc123...' },
    { id: 2, amount: 50, price: 9.5, currency: 'BTC', seller: '03def456...' },
    { id: 3, amount: 25, price: 4.8, currency: 'USDT', seller: '04ghi789...' },
  ])

  const [newListing, setNewListing] = useState({
    amount: '',
    price: '',
    currency: 'USDT'
  })

  const handleBuy = (product) => {
    if (!isConnected) {
      alert('Please connect to market first')
      return
    }
    console.log('Buying product:', product)
    alert(`Buying ${product.amount} SKY for ${product.price} ${product.currency}`)
  }

  const handleCreateListing = (e) => {
    e.preventDefault()
    if (!isConnected) {
      alert('Please connect to market first')
      return
    }
    console.log('Creating listing:', newListing)
    alert(`Sell order created: ${newListing.amount} SKY for ${newListing.price} ${newListing.currency}`)
    setShowCreateListing(false)
    setNewListing({ amount: '', price: '', currency: 'USDT' })
  }

  if (!isConnected) {
    return (
      <div className="alert alert-warning">
        <h4>⚠️ Not Connected</h4>
        <p>Please go to Settings and enter your wallet addresses and Market Public Key first.</p>
      </div>
    )
  }

  return (
    <div>
      <div className="d-flex justify-content-between align-items-center mb-4">
        <h2>Market</h2>
        <button 
          className="btn btn-primary"
          onClick={() => setShowCreateListing(!showCreateListing)}
        >
          {showCreateListing ? 'Cancel' : '+ Create Sell Order'}
        </button>
      </div>

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
                  value={newListing.amount}
                  onChange={(e) => setNewListing({...newListing, amount: e.target.value})}
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
                  onChange={(e) => setNewListing({...newListing, price: e.target.value})}
                  required
                  step="0.0001"
                  min="0"
                />
              </div>
              <div className="col-md-4">
                <label className="form-label">Payment Currency</label>
                <select 
                  className="form-select"
                  value={newListing.currency}
                  onChange={(e) => setNewListing({...newListing, currency: e.target.value})}
                >
                  <option value="USDT">USDT</option>
                  <option value="BTC">BTC</option>
                  <option value="BCH">BCH</option>
                  <option value="LTC">LTC</option>
                </select>
              </div>
            </div>
            <button type="submit" className="btn btn-primary">
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
        <div className="alert alert-info">
          No products available for sale.
        </div>
      ) : (
        <div className="row">
          {products.map((product) => (
            <div key={product.id} className="col-md-6 col-lg-4 mb-3">
              <div className="card product-card" onClick={() => handleBuy(product)}>
                <div className="product-amount mb-2">
                  <strong>{product.amount} SKY</strong>
                </div>
                <div className="product-price mb-2">
                  {product.price} {product.currency}
                </div>
                <div className="text-muted small mb-3">
                  Seller: {product.seller}
                </div>
                <button className="btn btn-primary w-100">
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

export default Market
