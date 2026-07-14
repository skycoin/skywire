import { useState } from 'react'
import { api } from '../api'

// Phase 1 trades SKY for BTC or LTC. A user registers a SKY address (to receive
// purchased SKY as a buyer, or refunds as a seller) and, if selling, a BTC/LTC
// address to be paid to. Other coins come later.
function Settings({ marketPubKey }) {
  const [wallets, setWallets] = useState({
    SKY: '',
    BTC: '',
    LTC: '',
  })
  const [saving, setSaving] = useState(false)
  const [message, setMessage] = useState(null)

  const handleWalletChange = (currency, value) => {
    setWallets({
      ...wallets,
      [currency]: value,
    })
  }

  const handleSaveWallets = async () => {
    setMessage(null)
    if (!wallets.SKY.trim()) {
      setMessage({ type: 'danger', text: 'A SKY wallet address is required.' })
      return
    }
    setSaving(true)
    try {
      await api.register({
        wallet_sky: wallets.SKY.trim(),
        wallet_btc: wallets.BTC.trim(),
        wallet_ltc: wallets.LTC.trim(),
      })
      setMessage({ type: 'success', text: 'Wallet addresses saved to the market.' })
    } catch (e) {
      setMessage({ type: 'danger', text: e.message })
    } finally {
      setSaving(false)
    }
  }

  return (
    <div>
      <h2 className="mb-4">Settings</h2>

      <div className="card mb-4">
        <h4>Market Connection</h4>
        <div className="mb-1">
          <label className="form-label">Connected Market Public Key</label>
          <input
            type="text"
            className="form-control"
            value={marketPubKey}
            readOnly
            disabled
          />
          <small className="text-muted">
            Use the Disconnect button in the top bar to switch markets.
          </small>
        </div>
      </div>

      <div className="card">
        <h4>Wallet Addresses</h4>
        <p className="text-muted">
          Your <strong>Skycoin</strong> address receives every purchased sell coin — SKY and any
          fibercoin (they share the Skycoin address format) — plus any refunds. To sell, add the
          <strong> BTC</strong> or <strong>LTC</strong> address you want buyers to pay. The market
          validates each address, so double-check for typos.
        </p>

        <div className="mb-3">
          <label className="form-label">Skycoin address <span className="text-danger">*</span></label>
          <input
            type="text"
            className="form-control"
            placeholder="Skycoin-family address (receives SKY and all fibercoins)"
            value={wallets.SKY}
            onChange={(e) => handleWalletChange('SKY', e.target.value)}
          />
        </div>

        <div className="mb-3">
          <label className="form-label">BTC</label>
          <input
            type="text"
            className="form-control"
            placeholder="Bitcoin payout address (for sellers)"
            value={wallets.BTC}
            onChange={(e) => handleWalletChange('BTC', e.target.value)}
          />
        </div>

        <div className="mb-3">
          <label className="form-label">LTC</label>
          <input
            type="text"
            className="form-control"
            placeholder="Litecoin payout address (for sellers)"
            value={wallets.LTC}
            onChange={(e) => handleWalletChange('LTC', e.target.value)}
          />
        </div>

        {message && (
          <div className={`alert alert-${message.type} py-2`}>{message.text}</div>
        )}

        <button
          className="btn btn-connect"
          onClick={handleSaveWallets}
          disabled={saving}
        >
          {saving ? 'Saving…' : 'Save Addresses'}
        </button>
      </div>
    </div>
  )
}

export default Settings
