import { useState } from 'react'

function Orders({ isConnected }) {
  // Mock data - will be replaced with real API calls
  const [orders] = useState([
    { 
      id: 'ord_001', 
      type: 'buy', 
      amount: 10, 
      price: 2.0, 
      currency: 'USDT',
      status: 'pending_payment',
      createdAt: '2026-07-10T10:30:00Z',
      expiresAt: '2026-07-10T10:45:00Z'
    },
    { 
      id: 'ord_002', 
      type: 'sell', 
      amount: 50, 
      price: 9.5, 
      currency: 'BTC',
      status: 'active',
      createdAt: '2026-07-10T09:00:00Z'
    },
    { 
      id: 'ord_003', 
      type: 'buy', 
      amount: 25, 
      price: 4.8, 
      currency: 'USDT',
      status: 'completed',
      createdAt: '2026-07-09T15:20:00Z'
    },
  ])

  const getStatusBadge = (status) => {
    const statusMap = {
      pending_payment: { label: 'Pending Payment', class: 'bg-warning' },
      active: { label: 'Active', class: 'bg-success' },
      completed: { label: 'Completed', class: 'bg-info' },
      cancelled: { label: 'Cancelled', class: 'bg-danger' },
      expired: { label: 'Expired', class: 'bg-secondary' },
    }
    const statusInfo = statusMap[status] || { label: status, class: 'bg-secondary' }
    return <span className={`badge ${statusInfo.class}`}>{statusInfo.label}</span>
  }

  const getTypeBadge = (type) => {
    return type === 'buy' 
      ? <span className="badge bg-primary">Buy</span>
      : <span className="badge bg-info">Sell</span>
  }

  if (!isConnected) {
    return (
      <div className="alert alert-warning">
        <h4>⚠️ Not Connected</h4>
        <p>Please connect to the market first.</p>
      </div>
    )
  }

  return (
    <div>
      <h2 className="mb-4">My Orders</h2>

      {orders.length === 0 ? (
        <div className="alert alert-info">
          You don't have any orders yet.
        </div>
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
                <th>Date</th>
              </tr>
            </thead>
            <tbody>
              {orders.map((order) => (
                <tr key={order.id}>
                  <td><code>{order.id}</code></td>
                  <td>{getTypeBadge(order.type)}</td>
                  <td>{order.amount} SKY</td>
                  <td>{order.price} {order.currency}</td>
                  <td>{getStatusBadge(order.status)}</td>
                  <td>{new Date(order.createdAt).toLocaleString()}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  )
}

export default Orders
