import { useEffect, useState, useCallback } from 'react'
import { api } from '../api'

// MyListings shows the seller their own pending sell listings and tracks each
// one's lifecycle live: pending (awaiting the SKY deposit) -> confirmed (deposit
// detected on-chain; the listing is now a purchasable product on the market).
function MyListings() {
  const [listings, setListings] = useState([])
  const [marketWallet, setMarketWallet] = useState('')
  const [error, setError] = useState('')

  const load = useCallback(async () => {
    try {
      const data = await api.getListings()
      setListings(data.listings || [])
      setMarketWallet(data.market_wallet || '')
      setError('')
    } catch (e) {
      setError(e.message)
    }
  }, [])

  useEffect(() => {
    load()
    const id = setInterval(load, 5000)
    return () => clearInterval(id)
  }, [load])

  // The three lifecycle steps, with the current one highlighted.
  const steps = ['pending', 'confirmed', 'product']
  const stepIndex = (status) => {
    if (status === 'pending') return 0
    if (status === 'confirmed') return 1 // deposit seen -> product created
    return 2
  }

  const renderLifecycle = (status) => {
    const active = stepIndex(status)
    const labels = ['Pending deposit', 'Confirmed', 'Listed as product']
    return (
      <div className="d-flex align-items-center gap-2 flex-wrap">
        {steps.map((_, i) => (
          <span key={i} className="d-flex align-items-center gap-2">
            <span
              className={`badge ${
                i < active ? 'bg-success' : i === active ? 'bg-info' : 'bg-secondary'
              }`}
            >
              {labels[i]}
            </span>
            {i < steps.length - 1 && <span className="text-muted">→</span>}
          </span>
        ))}
      </div>
    )
  }

  return (
    <div>
      <h2 className="mb-4">My Listings</h2>

      {error && <div className="alert alert-danger">{error}</div>}

      {listings.length === 0 ? (
        <div className="alert alert-secondary">
          You have no active sell listings. Create one from the Market tab.
        </div>
      ) : (
        <div className="table-responsive">
          <table className="table table-dark table-hover align-middle">
            <thead>
              <tr>
                <th>ID</th>
                <th>Amount</th>
                <th>Price</th>
                <th>Deposit</th>
                <th>Lifecycle</th>
                <th>Deposit Tx</th>
                <th>Expires</th>
              </tr>
            </thead>
            <tbody>
              {listings.map((l) => (
                <tr key={l.id}>
                  <td><code>{shorten(l.id)}</code></td>
                  <td>{l.amount_sky} SKY</td>
                  <td>{l.price} {l.payment_currency}</td>
                  <td>
                    {l.status === 'pending' ? (
                      <span title={marketWallet ? `to ${marketWallet}` : undefined}>
                        <strong>{l.expected_amount_sky}</strong> SKY
                      </span>
                    ) : (
                      <span className="text-muted">received</span>
                    )}
                  </td>
                  <td>{renderLifecycle(l.status)}</td>
                  <td>{l.tx_hash ? <code title={l.tx_hash}>{shorten(l.tx_hash)}</code> : <span className="text-muted">—</span>}</td>
                  <td>
                    {l.status === 'pending' && l.expires_at
                      ? new Date(l.expires_at).toLocaleTimeString()
                      : '-'}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      {marketWallet && listings.some((l) => l.status === 'pending') && (
        <div className="alert alert-info mt-3">
          For each pending listing, transfer the exact deposit amount shown to the market
          wallet <code>{marketWallet}</code> before it expires.
        </div>
      )}
    </div>
  )
}

function shorten(id) {
  if (!id || id.length <= 12) return id
  return `${id.slice(0, 8)}…`
}

export default MyListings
