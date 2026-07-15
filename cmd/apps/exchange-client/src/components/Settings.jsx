import { useState } from 'react'
import { api, loadWallets, saveWallets } from '../api'

// Settings is where a user registers the wallet addresses the market delivers to
// and pays out from. A Skycoin address is required (it receives every purchased
// sell coin — SKY and any fibercoin — plus refunds). To sell for BTC/LTC, add the
// matching payout address. Values are remembered locally and pre-filled here.
function Settings({ marketPubKey, onRegistered }) {
  const saved = loadWallets()
  const [wallets, setWallets] = useState({
    SKY: saved.SKY || '',
    BTC: saved.BTC || '',
    LTC: saved.LTC || '',
  })
  const [saving, setSaving] = useState(false)
  const [message, setMessage] = useState(null)

  const setField = (currency, value) => setWallets((w) => ({ ...w, [currency]: value }))

  const handleSave = async () => {
    setMessage(null)
    if (!wallets.SKY.trim()) {
      setMessage({ type: 'danger', text: 'A Skycoin address is required.' })
      return
    }
    setSaving(true)
    try {
      await api.register({
        wallet_sky: wallets.SKY.trim(),
        wallet_btc: wallets.BTC.trim(),
        wallet_ltc: wallets.LTC.trim(),
      })
      saveWallets({ SKY: wallets.SKY.trim(), BTC: wallets.BTC.trim(), LTC: wallets.LTC.trim() })
      setMessage({ type: 'success', text: 'Wallet addresses saved. You can now trade.' })
      if (onRegistered) onRegistered()
    } catch (e) {
      setMessage({ type: 'danger', text: e.message })
    } finally {
      setSaving(false)
    }
  }

  return (
    <div>
      <div className="page-head">
        <h2>Settings</h2>
      </div>

      <div className="panel">
        <h4 className="panel-title">Your wallet addresses</h4>
        <p className="text-muted mb-3">
          Register the addresses the market uses for you. Your <strong>Skycoin</strong> address
          receives every purchased sell coin (SKY and any fibercoin share the format) and any refund.
          Add a <strong>BTC</strong> or <strong>LTC</strong> payout address if you want to sell for
          those. Each address is validated by the market.
        </p>

        <div className="field-grid">
          <div>
            <label className="form-label">Skycoin address <span className="req">*</span></label>
            <input
              type="text"
              className="form-control"
              placeholder="Skycoin-family address (receives SKY + all fibercoins)"
              value={wallets.SKY}
              onChange={(e) => setField('SKY', e.target.value)}
            />
          </div>
          <div>
            <label className="form-label">BTC payout address</label>
            <input
              type="text"
              className="form-control"
              placeholder="Bitcoin address (for selling)"
              value={wallets.BTC}
              onChange={(e) => setField('BTC', e.target.value)}
            />
          </div>
          <div>
            <label className="form-label">LTC payout address</label>
            <input
              type="text"
              className="form-control"
              placeholder="Litecoin address (for selling)"
              value={wallets.LTC}
              onChange={(e) => setField('LTC', e.target.value)}
            />
          </div>
        </div>

        {message && <div className={`alert alert-${message.type} mt-3 mb-0`}>{message.text}</div>}

        <button className="btn btn-connect mt-3" onClick={handleSave} disabled={saving}>
          {saving ? 'Saving…' : 'Save addresses'}
        </button>
      </div>

      <div className="panel">
        <h4 className="panel-title">Market connection</h4>
        <label className="form-label">Connected market public key</label>
        <input type="text" className="form-control" value={marketPubKey} readOnly />
        <div className="hint mt-1">Use Disconnect in the top bar to switch markets.</div>
      </div>
    </div>
  )
}

export default Settings
