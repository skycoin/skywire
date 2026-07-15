import { useEffect, useState, useCallback } from 'react'
import { api } from '../api'

// Terminal states: a trade that is done (settled, or ended without settling).
const TERMINAL = new Set(['completed', 'canceled', 'cancelled', 'expired'])

// History lists the user's finished trades — both buys and sells — that are no
// longer in flight. Active orders live under My Orders / My Listings.
function History() {
  const [rows, setRows] = useState([])
  const [error, setError] = useState('')

  const load = useCallback(async () => {
    try {
      const data = await api.getOrders()
      const all = (data.orders || []).filter((o) => TERMINAL.has(o.status))
      all.sort((a, b) => new Date(b.created_at) - new Date(a.created_at))
      setRows(all)
      setError('')
    } catch (e) {
      setError(e.message)
    }
  }, [])

  useEffect(() => {
    load()
    const id = setInterval(load, 8000)
    return () => clearInterval(id)
  }, [load])

  const badge = (status) => {
    const cls =
      status === 'completed' ? 'enabled' : status === 'expired' ? 'disabled' : 'cancelled'
    return <span className={`badge ${cls}`}>{status}</span>
  }
  const typeBadge = (type) => (
    <span className={`badge ${type === 'buy' ? 'buy' : 'sell'}`}>{type}</span>
  )

  return (
    <div>
      <div className="page-head">
        <h2>History</h2>
      </div>

      {error && <div className="alert alert-danger">{error}</div>}

      {rows.length === 0 ? (
        <div className="alert alert-secondary">No completed trades yet.</div>
      ) : (
        <div className="panel table-wrap">
          <table className="table table-hover align-middle">
            <thead>
              <tr>
                <th>Type</th>
                <th>Coin</th>
                <th>Amount</th>
                <th>Price</th>
                <th>Status</th>
                <th>Date</th>
              </tr>
            </thead>
            <tbody>
              {rows.map((o) => (
                <tr key={o.id}>
                  <td>{typeBadge(o.type)}</td>
                  <td>{o.sell_coin}</td>
                  <td>{o.amount} {o.sell_coin}</td>
                  <td>{o.price} {o.payment_currency}</td>
                  <td>{badge(o.status)}</td>
                  <td>{o.created_at ? new Date(o.created_at).toLocaleString() : '-'}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  )
}

export default History
