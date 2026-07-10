import { useState } from 'react'
import Header from './components/Header'
import Market from './components/Market'
import Orders from './components/Orders'
import Settings from './components/Settings'

function App() {
  const [activeTab, setActiveTab] = useState('market')
  const [isConnected, setIsConnected] = useState(false)
  const [marketPubKey, setMarketPubKey] = useState('')

  return (
    <div className="app-container">
      <Header 
        isConnected={isConnected} 
        marketPubKey={marketPubKey}
        setIsConnected={setIsConnected}
        setMarketPubKey={setMarketPubKey}
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
          <Settings 
            isConnected={isConnected}
            marketPubKey={marketPubKey}
            setMarketPubKey={setMarketPubKey}
            setIsConnected={setIsConnected}
          />
        )}
      </div>
    </div>
  )
}

export default App
