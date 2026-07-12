import { useState } from 'react'

function Settings({ marketPubKey }) {
  const [wallets, setWallets] = useState({
    SKY: '',
    BTC: '',
    BCH: '',
    LTC: '',
    USDT_TRC20: '',
    USDT_ERC20: ''
  })

  const handleWalletChange = (currency, value) => {
    setWallets({
      ...wallets,
      [currency]: value
    })
  }

  const handleSaveWallets = () => {
    console.log('Saving wallets:', wallets)
    alert('Wallet addresses saved')
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

        <button 
          className="btn btn-primary"
          onClick={handleSaveWallets}
        >
          Save Addresses
        </button>
      </div>
    </div>
  )
}

export default Settings
