import { Injectable } from '@angular/core';
import { HttpEvent, HttpHandler, HttpInterceptor, HttpRequest } from '@angular/common/http';
import { Observable } from 'rxjs';

/** Where a wallet request goes instead, and the backend it selects. */
export interface WalletBackendTarget {
  url: string;
  headers: Record<string, string>;
}

/** The electrum server the BTC coin uses when none is configured. */
const defaultBtcBackend = 'ssl://electrum.blockstream.info:50002';

function stored(key: string): string {
  try {
    return localStorage.getItem(key) || '';
  } catch (e) {
    return '';
  }
}

/**
 * Maps a request the wallet makes to the hypervisor's wallet routes
 * (pkg/visor/hypervisor_handlers_wallet.go). The wallet addresses its coin list
 * as /api/v1/coins and each coin's node as /coin/<index>/api/..., or as a node
 * URL the user entered. Here those become wallet/coins and wallet/coin/<index>/...
 * relative to the page, so they stay under the desk's /vnet/<port>/ prefix,
 * and the chosen backend travels in a header: the BTC electrum server and
 * skysocks exit for bitcoin, the node host for a skycoin-style coin. These are
 * the rules the iframed wallet's fetch shim applied, with the same
 * localStorage keys, so a wallet configured there works unchanged.
 *
 * Returns null for anything else, which passes through untouched.
 */
export function walletBackendTarget(url: string, page: string = location.href): WalletBackendTarget | null {
  let parsed: URL;
  try {
    parsed = new URL(url, page);
  } catch (e) {
    return null;
  }
  const path = parsed.pathname;
  if (/\/api\/v1\/coins$/.test(path)) {
    return { url: 'wallet/coins', headers: {} };
  }

  const coin = /\/coin\/(\d+)\//.exec(path);
  let target: string;
  if (coin) {
    target = 'wallet' + path.slice(path.indexOf('/coin/')) + parsed.search;
  } else if (/^\/(wallet\/)?api\/v[12]\//.test(path)) {
    target = 'wallet/coin/0/' + path.replace(/^\/(wallet\/)?/, '') + parsed.search;
  } else {
    return null;
  }

  const headers: Record<string, string> = {};
  if (/\/v1\/btc\//.test(path)) {
    // The electrum server comes from the wallet's own node settings, which
    // keep the bitcoin coin (id -2) in localStorage nodeUrls.
    let backend = '';
    try {
      backend = (JSON.parse(stored('nodeUrls') || '{}') || {})['-2'] || '';
    } catch (e) {}
    headers['X-Skywire-Btc-Backend'] = backend || stored('skywire-btc-backend') || defaultBtcBackend;
    const exit = stored('skywire-btc-proxy');
    if (exit) {
      headers['X-Skywire-Btc-Proxy'] = exit;
    }
  } else {
    const pageHost = new URL(page).host;
    const node = parsed.host && parsed.host !== pageHost ? parsed.protocol + '//' + parsed.host : stored('skywire-coin-node');
    if (node) {
      headers['X-Skywire-Coin-Node'] = node;
    }
  }

  return { url: target, headers: headers };
}

/** Sends the wallet's node API calls to the hypervisor, as walletBackendTarget maps them. */
@Injectable()
export class WalletBackendInterceptor implements HttpInterceptor {
  intercept(req: HttpRequest<unknown>, next: HttpHandler): Observable<HttpEvent<unknown>> {
    const target = walletBackendTarget(req.url);

    return next.handle(target ? req.clone({ url: target.url, setHeaders: target.headers }) : req);
  }
}
