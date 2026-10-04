import { HTTP_INTERCEPTORS } from '@angular/common/http';
import { Route } from '@angular/router';

import { WALLET_BASE_PATH } from '../../skycoin-wallet/app/wallet-path';
import { AuthGuardService } from '../services/auth-guard.service';
import { WalletBackendInterceptor } from './wallet-backend.interceptor';
import { WalletPageComponent } from './wallet-page.component';

/** Adds a script or stylesheet to the page once, resolving when it has loaded. */
function addToPage(tag: 'script' | 'link', url: string): Promise<void> {
  const attr = tag === 'script' ? 'src' : 'href';
  if (document.querySelector(`${tag}[${attr}="${url}"]`)) {
    return Promise.resolve();
  }

  return new Promise((resolve, reject) => {
    const el = document.createElement(tag);
    if (el instanceof HTMLLinkElement) {
      el.rel = 'stylesheet';
      el.href = url;
    } else {
      el.src = url;
    }
    el.onload = () => resolve();
    el.onerror = () => reject(new Error('could not load ' + url));
    document.head.appendChild(el);
  });
}

/**
 * Loads the skycoin-web wallet. Besides its module it needs a few page-wide
 * things, added only when the wallet is first opened: its stylesheet, the
 * QR code library, Go's wasm loader for its cipher (the hypervisor serves
 * that and the TinyGo cipher itself), and the Buffer global bip39 expects.
 */
export async function loadSkycoinWallet() {
  await Promise.all([
    addToPage('link', 'skycoin-wallet.css'),
    addToPage('script', 'skycoin-wallet-qrcode.js'),
    addToPage('script', 'assets/scripts/wasm_exec.js'),
    import('buffer').then((m) => {
      // A CommonJS module: its exports may arrive as the namespace or under default.
      const exports = m as unknown as { Buffer?: unknown; default?: { Buffer?: unknown } };
      const w = window as unknown as { Buffer?: unknown };
      w.Buffer = w.Buffer || exports.Buffer || exports.default?.Buffer;
    }),
  ]);

  return (await import('../../skycoin-wallet/app/wallet.module')).SkycoinWalletModule;
}

/** The #/wallet route. Its providers reach the wallet module loaded under it. */
export const walletRoute: Route = {
  path: 'wallet',
  canActivate: [AuthGuardService],
  component: WalletPageComponent,
  providers: [
    { provide: WALLET_BASE_PATH, useValue: '/wallet' },
    { provide: HTTP_INTERCEPTORS, useClass: WalletBackendInterceptor, multi: true },
  ],
  children: [{ path: '', loadChildren: loadSkycoinWallet }],
};
