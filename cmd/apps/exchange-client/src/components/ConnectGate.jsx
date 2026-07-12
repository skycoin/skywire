import { useState } from 'react'

// ConnectGate is the first screen the user sees. It shows the market public key
// (pre-filled from the --market-pk config value), lets the user confirm or edit
// it, and connects only when the user clicks Connect. The client never connects
// automatically.
function ConnectGate({ defaultPubKey, onConnected }) {
  const [pubKey, setPubKey] = useState(defaultPubKey || '')
  const [connecting, setConnecting] = useState(false)
  const [error, setError] = useState('')

  async function handleConnect(e) {
    e.preventDefault()
    setError('')
    const pk = pubKey.trim()
    if (!pk) {
      setError('Please enter the market public key.')
      return
    }
    setConnecting(true)
    try {
      const res = await fetch('/api/connect', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ market_pk: pk }),
      })
      const data = await res.json()
      if (!res.ok) {
        setError(data.error || 'Failed to connect to the market.')
        return
      }
      onConnected(data.market_pk || pk)
    } catch (err) {
      setError('Failed to reach the market: ' + err.message)
    } finally {
      setConnecting(false)
    }
  }

  return (
    <div className="app-container d-flex align-items-center justify-content-center">
      <div className="connect-card">
        <h3 className="mb-1 text-center">Skywire Exchange</h3>
        <p className="text-center text-muted mb-4">Connect to a market to start trading</p>

        <form onSubmit={handleConnect}>
          <label className="form-label">Market Public Key</label>
          <input
            type="text"
            className="form-control mb-2"
            placeholder="Enter the exchange-market public key"
            value={pubKey}
            onChange={(e) => setPubKey(e.target.value)}
            disabled={connecting}
            autoFocus
          />

          {error && <div className="alert alert-danger py-2">{error}</div>}

          <button
            type="submit"
            className="btn btn-connect w-100"
            disabled={connecting}
          >
            {connecting ? 'Connecting…' : 'Connect'}
          </button>
        </form>
      </div>
    </div>
  )
}

export default ConnectGate
