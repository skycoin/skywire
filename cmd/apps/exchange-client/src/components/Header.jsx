function Header({ isConnected, marketPubKey }) {
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
      </div>
    </header>
  )
}

export default Header
