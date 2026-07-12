function Header({ isConnected, marketPubKey, onDisconnect }) {
  return (
    <header className="header">
      <div className="status-bar">
        <div className="status-indicator">
          <div className={`status-dot ${isConnected ? 'connected' : ''}`}></div>
          <span className="fw-bold">
            {isConnected ? 'Connected to Market' : 'Disconnected'}
          </span>
        </div>

        {marketPubKey && (
          <div className="market-pubkey">
            <small>Market Public Key:</small>
            <br />
            <code>{marketPubKey}</code>
          </div>
        )}

        {isConnected && (
          <button className="btn btn-sm btn-outline-light" onClick={onDisconnect}>
            Disconnect
          </button>
        )}
      </div>
    </header>
  )
}

export default Header
