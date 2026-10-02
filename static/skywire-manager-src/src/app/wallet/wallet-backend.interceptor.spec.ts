import { walletBackendTarget } from './wallet-backend.interceptor';

describe('walletBackendTarget', () => {
  const page = 'https://127.0.0.1:8000/#/wallet';
  const deskPage = 'https://mesh.localhost:8443/vnet/8001/#/wallet';

  afterEach(() => {
    localStorage.removeItem('skywire-coin-node');
    localStorage.removeItem('skywire-btc-proxy');
    localStorage.removeItem('nodeUrls');
  });

  it('sends the coin list to the hypervisor registry', () => {
    expect(walletBackendTarget('/api/v1/coins', page)).toEqual({ url: 'wallet/coins', headers: {} });
  });

  it('keeps a coin index and query, relative to the page', () => {
    const t = walletBackendTarget('/coin/1/api/v1/balance?addrs=a,b', page);
    expect(t?.url).toBe('wallet/coin/1/api/v1/balance?addrs=a,b');
    expect(walletBackendTarget('/coin/1/api/v1/balance', deskPage)?.url).toBe('wallet/coin/1/api/v1/balance');
  });

  it('sends a bare node API call to coin 0', () => {
    expect(walletBackendTarget('/api/v2/transaction', page)?.url).toBe('wallet/coin/0/api/v2/transaction');
  });

  it('names a node the user entered, else the configured one', () => {
    const custom = walletBackendTarget('https://node.example.com/api/v1/health', page);
    expect(custom?.url).toBe('wallet/coin/0/api/v1/health');
    expect(custom?.headers['X-Skywire-Coin-Node']).toBe('https://node.example.com');

    localStorage.setItem('skywire-coin-node', 'node.skycoin.com.dmsg');
    expect(walletBackendTarget('/coin/0/api/v1/health', page)?.headers['X-Skywire-Coin-Node']).toBe('node.skycoin.com.dmsg');
  });

  it('gives a bitcoin query its electrum server and exit', () => {
    expect(walletBackendTarget('/coin/2/api/v1/btc/balance', page)?.headers)
      .toEqual({ 'X-Skywire-Btc-Backend': 'ssl://electrum.blockstream.info:50002' });

    localStorage.setItem('nodeUrls', JSON.stringify({ '-2': 'ssl://electrum.example.com:50002' }));
    localStorage.setItem('skywire-btc-proxy', 'exitpk');
    expect(walletBackendTarget('/coin/2/api/v1/btc/balance', page)?.headers).toEqual({
      'X-Skywire-Btc-Backend': 'ssl://electrum.example.com:50002',
      'X-Skywire-Btc-Proxy': 'exitpk',
    });
  });

  it('leaves everything else alone', () => {
    expect(walletBackendTarget('assets/skycoin-wallet/img/logo.png', page)).toBeNull();
    expect(walletBackendTarget('https://api.coinpaprika.com/v1/tickers/sky-skycoin', page)).toBeNull();
  });
});
