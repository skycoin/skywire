import { useEffect, useState, useCallback } from 'react'
import { api } from '../api'

function Orders() {
  const [orders, setOrders] = useState([])
  const [error, setError] = useState('')

  const load = useCallback(async () => {
    try {
      const data = await api.getOrders()
      setOrders(data.orders || [])
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

  const getStatusBadge = (status) => {
    const statusMap = {
      pending_payment: { label: 'Pending Payment', class: 'bg-warning text-dark' },
      paid: { label: 'Paid', class: 'bg-info' },
      confirmed: { label: 'Confirmed', class: 'bg-info' },
      active: { label: 'Active', class: 'bg-success' },
      completed: { label: 'Completed', class: 'bg-success' },
      cancelled: { label: 'Cancelled', class: 'bg-danger' },
      expired: { label: 'Expired', class: 'bg-secondary' },
      pending: { label: 'Pending', class: 'bg-warning text-dark' },
    }
    const info = statusMap[status] || { label: status, class: 'bg-secondary' }
    return <span className={`badge ${info.class}`}>{info.label}</span>
  }

  const getTypeBadge = (type) =>
    type === 'buy' ? (
      <span className="badge bg-primary">Buy</span>
    ) : (
      <span className="badge bg-info">Sell</span>
    )

  return (
    <div>
      <h2 className="mb-4">My Orders</h2>

      {error && <div className="alert alert-danger">{error}</div>}

      {orders.length === 0 ? (
        <div className="alert alert-secondary">You don't have any orders yet.</div>
      ) : (
        <div className="table-responsive">
          <table className="table table-dark table-hover">
            <thead>
              <tr>
                <th>ID</th>
                <th>Type</th>
                <th>Amount</th>
                <th>Price</th>
                <th>Status</th>
                <th>Created</th>
              </tr>
            </thead>
            <tbody>
              {orders.map((order) => (
                <tr key={order.id}>
                  <td><code>{shorten(order.id)}</code></td>
                  <td>{getTypeBadge(order.type)}</td>
                  <td>{order.amount_sky} SKY</td>
                  <td>{order.price} {order.payment_currency}</td>
                  <td>{getStatusBadge(order.status)}</td>
                  <td>{order.created_at ? new Date(order.created_at).toLocaleString() : '-'}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  )
}

function shorten(id) {
  if (!id || id.length <= 12) return id
  return `${id.slice(0, 8)}…`
}

export default Orders
