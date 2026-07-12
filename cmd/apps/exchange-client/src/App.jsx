import { useEffect, useState } from 'react'
import Header from './components/Header'
import Market from './components/Market'
import Orders from './components/Orders'
import Settings from './components/Settings'
import ConnectGate from './components/ConnectGate'

function App() {
  const [activeTab, setActiveTab] = useState('market')
  const [isConnected, setIsConnected] = useState(false)
  const [marketPubKey, setMarketPubKey] = useState('')
  // defaultPubKey is the --market-pk value; used to pre-fill the connect input.
  const [defaultPubKey, setDefaultPubKey] = useState('')
  const [loading, setLoading] = useState(true)

  // On load, read the configured default market key and current status.
  useEffect(() => {
    async function init() {
      try {
        const [cfg, status] = await Promise.all([
          fetch('/api/config').then((r) => r.json()),
          fetch('/api/status').then((r) => r.json()),
        ])
        setDefaultPubKey(cfg.market_pk || '')
        if (status.connected) {
          setIsConnected(true)
          setMarketPubKey(status.market_pk || '')
        }
      } catch (e) {
        // Leave the gate visible; the user can still try to connect.
        console.error('init failed', e)
      } finally {
        setLoading(false)
      }
    }
    init()
  }, [])

  function handleConnected(pk) {
    setMarketPubKey(pk)
    setIsConnected(true)
  }

  async function handleDisconnect() {
    try {
      await fetch('/api/disconnect', { method: 'POST' })
    } catch (e) {
      console.error('disconnect failed', e)
    }
    setIsConnected(false)
    setMarketPubKey('')
  }

  if (loading) {
    return (
      <div className="app-container d-flex align-items-center justify-content-center">
        <div className="spinner-border text-light" role="status" />
      </div>
    )
  }

  // First step: the connection gate. The app is only shown once connected.
  if (!isConnected) {
    return <ConnectGate defaultPubKey={defaultPubKey} onConnected={handleConnected} />
  }

  return (
    <div className="app-container">
      <Header
        isConnected={isConnected}
        marketPubKey={marketPubKey}
        onDisconnect={handleDisconnect}
      />

      <div className="container-fluid">
        <ul className="nav nav-tabs mt-3">
          <li className="nav-item">
            <button
              className={`nav-link ${activeTab === 'market' ? 'active' : ''}`}
              onClick={() => setActiveTab('market')}
            >
              Market
            </button>
          </li>
          <li className="nav-item">
            <button
              className={`nav-link ${activeTab === 'orders' ? 'active' : ''}`}
              onClick={() => setActiveTab('orders')}
            >
              My Orders
            </button>
          </li>
          <li className="nav-item">
            <button
              className={`nav-link ${activeTab === 'settings' ? 'active' : ''}`}
              onClick={() => setActiveTab('settings')}
            >
              Settings
            </button>
          </li>
        </ul>
      </div>

      <div className="content">
        {activeTab === 'market' && <Market isConnected={isConnected} />}
        {activeTab === 'orders' && <Orders isConnected={isConnected} />}
        {activeTab === 'settings' && (
          <Settings isConnected={isConnected} marketPubKey={marketPubKey} />
        )}
      </div>
    </div>
  )
}

export default App
