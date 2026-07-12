import { useState } from 'react'
import { api } from '../api'

function Settings({ marketPubKey }) {
  const [wallets, setWallets] = useState({
    SKY: '',
    BTC: '',
    BCH: '',
    LTC: '',
    USDT_TRC20: '',
    USDT_ERC20: ''
  })
  const [saving, setSaving] = useState(false)
  const [message, setMessage] = useState(null)

  const handleWalletChange = (currency, value) => {
    setWallets({
      ...wallets,
      [currency]: value
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
        wallet_bch: wallets.BCH.trim(),
        wallet_ltc: wallets.LTC.trim(),
        wallet_usdt_erc20: wallets.USDT_ERC20.trim(),
        wallet_usdt_trc20: wallets.USDT_TRC20.trim(),
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
          Enter your wallet addresses for receiving and sending currencies
        </p>

        <div className="mb-3">
          <label className="form-label">SKY</label>
          <input 
            type="text" 
            className="form-control"
            placeholder="SKY wallet address"
            value={wallets.SKY}
            onChange={(e) => handleWalletChange('SKY', e.target.value)}
          />
        </div>

        <div className="mb-3">
          <label className="form-label">BTC</label>
          <input 
            type="text" 
            className="form-control"
            placeholder="Bitcoin wallet address"
            value={wallets.BTC}
            onChange={(e) => handleWalletChange('BTC', e.target.value)}
          />
        </div>

        <div className="mb-3">
          <label className="form-label">BCH</label>
          <input 
            type="text" 
            className="form-control"
            placeholder="Bitcoin Cash wallet address"
            value={wallets.BCH}
            onChange={(e) => handleWalletChange('BCH', e.target.value)}
          />
        </div>

        <div className="mb-3">
          <label className="form-label">LTC</label>
          <input 
            type="text" 
            className="form-control"
            placeholder="Litecoin wallet address"
            value={wallets.LTC}
            onChange={(e) => handleWalletChange('LTC', e.target.value)}
          />
        </div>

        <div className="mb-3">
          <label className="form-label">USDT (TRC20)</label>
          <input 
            type="text" 
            className="form-control"
            placeholder="Tether wallet address (TRON network)"
            value={wallets.USDT_TRC20}
            onChange={(e) => handleWalletChange('USDT_TRC20', e.target.value)}
          />
        </div>

        <div className="mb-3">
          <label className="form-label">USDT (ERC20)</label>
          <input 
            type="text" 
            className="form-control"
            placeholder="Tether wallet address (Ethereum network)"
            value={wallets.USDT_ERC20}
            onChange={(e) => handleWalletChange('USDT_ERC20', e.target.value)}
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
