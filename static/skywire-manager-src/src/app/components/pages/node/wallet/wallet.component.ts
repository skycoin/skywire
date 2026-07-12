import { Component, OnDestroy, OnInit } from '@angular/core';
import { Subscription } from 'rxjs';
import { DomSanitizer, SafeResourceUrl } from '@angular/platform-browser';

import { Node, Application } from '../../../../app.datatypes';
import { NodeComponent } from '../node.component';
import { PageBaseComponent } from 'src/app/utils/page-base';
import { AppsService } from 'src/app/services/apps.service';
import { SnackbarService } from 'src/app/services/snackbar.service';

const SKYCOIN_DAEMON_PREFIX = 'skycoin-daemon';

/**
 * Per-visor Wallet tab. Iframes the hypervisor-served wallet at the
 * same-origin path `/wallet/` — the HV serves the static skycoin-web
 * bundle and proxies its node API over the visor's dmsg client
 * (pkg/visor/hypervisor_handlers_wallet.go), so the wallet is ALWAYS
 * available with no skycoin-web process, no port, and works for remote
 * hypervisors (same-origin, not a cross-host `:8002`). Identical URL on
 * the native and wasm visors — see docs/design/gui-app-serving-modes.md.
 *
 * The skycoin-web *app* (own port, disk wallets, server-side multi-coin)
 * remains an opt-in "power" mode: when it's configured on the visor the
 * header exposes start/stop + settings, but it no longer gates the
 * inline wallet.
 */
@Component({
  selector: 'app-wallet',
  templateUrl: './wallet.component.html',
  styleUrls: ['./wallet.component.scss'],
  standalone: false,
})
export class WalletComponent extends PageBaseComponent implements OnInit, OnDestroy {
  node: Node;
  // Possible UI states. The template branches on these.
  state: 'unknown' | 'not-configured' | 'not-running' | 'running' = 'unknown';
  // The URL the iframe / "open in new tab" button points at when
  // state === 'running'. Built from whatever --host/--port the
  // skycoin-web app is configured with, falling back to the upstream
  // defaults when those flags aren't passed.
  iframeUrl: SafeResourceUrl | null = null;
  fullWindowUrl = '';

  // ---- coin-backend config (the wallet's "which node" pre-wallet setting) ----
  // The node the HV-served wallet queries over dmsg, "<pk>:<port>". Stored in
  // localStorage['skywire-coin-node'] — the SAME key the wasm fetchDmsg shim and
  // the native /wallet/ shim (X-Skywire-Coin-Node) read, so config is unified
  // across visors and settable BEFORE any wallet exists. Empty = deployment
  // default. This is the first field of what grows into the multi-coin + BTC
  // backends panel (docs/design/gui-app-serving-modes.md).
  static readonly COIN_NODE_KEY = 'skywire-coin-node';
  coinNode = '';
  backendOpen = false;
  nodeError = '';
  // Last node PK we built the iframe URL for. NodeComponent.currentNode
  // emits on every polling refresh; rebuilding the SafeResourceUrl on
  // every tick reloads the iframe and tears down whatever the wallet
  // had open (mirrors the bug we fixed on the terminal tab earlier).
  private boundPk = '';

  // The skycoin-web app entry from this.node.apps, when present.
  // Drives both the wallet header (start/stop/settings) and the
  // iframe state above. Null when the app isn't configured on the
  // visor at all (a "not-configured" template state results).
  webApp: Application | null = null;
  webAppBusy = false;
  webAppSettingsOpen = false;

  // Skycoin daemon instances on this visor — the wallet is a
  // thin-client of one or more daemons, so they live on the same
  // tab. Multi-instance: one daemon per fiberchain.
  daemons: Application[] = [];
  daemonsBusy = new Set<string>();
  // Names of daemons whose settings panel is currently expanded.
  // Multiple can be open at once — the panels are inline so they
  // don't fight for focus the way dialogs do.
  expandedDaemons = new Set<string>();

  private nodeSub: Subscription;

  constructor(
    private sanitizer: DomSanitizer,
    private appsService: AppsService,
    private snackbar: SnackbarService,
  ) {
 super(); 
}

  ngOnInit() {
    // Load the configured coin node ONCE (not in recompute — that runs every
    // poll tick and would clobber the field while the user is editing it).
    try {
      this.coinNode = localStorage.getItem(WalletComponent.COIN_NODE_KEY) || '';
    } catch (e) { /* private-mode / disabled storage */ }

    this.nodeSub = NodeComponent.currentNode.subscribe((node: Node) => {
      this.node = node;
      this.recompute();
    });

    return super.ngOnInit();
  }

  ngOnDestroy(): void {
    this.nodeSub?.unsubscribe();
  }

  toggleBackend() {
    this.backendOpen = !this.backendOpen;
  }

  /** Saves the coin-node selection (or clears it for the default) and reloads
   *  the wallet iframe so it picks up the new backend. `val` comes straight
   *  from the input (template ref) — no FormsModule dependency. */
  saveCoinNode(val: string) {
    const v = (val || '').trim().replace(/^dmsg:\/\//, '');
    if (v && !/^[0-9a-fA-F]{66}:\d+$/.test(v)) {
      this.nodeError = 'Expected <66-hex-pk>:<port> (a dmsg node), or empty for the default.';
      return;
    }
    this.nodeError = '';
    try {
      if (v) {
        localStorage.setItem(WalletComponent.COIN_NODE_KEY, v);
      } else {
        localStorage.removeItem(WalletComponent.COIN_NODE_KEY);
      }
    } catch (e) { /* storage unavailable */ }
    this.coinNode = v;
    this.reloadWallet();
  }

  /** Recreates the iframe so it re-fetches against the new coin node. */
  private reloadWallet() {
    this.iframeUrl = null;
    // Re-assign on the next tick so Angular tears down + rebuilds the iframe.
    setTimeout(() => {
      this.iframeUrl = this.sanitizer.bypassSecurityTrustResourceUrl('/wallet/?_=' + Date.now());
    }, 0);
  }

  /** Re-evaluates the UI state from the latest node snapshot. Cheap;
   *  safe to call on every NodeComponent polling tick. */
  private recompute() {
    if (!this.node) {
 this.state = 'unknown';

 return; 
}

    const apps = (this.node.apps || []) as Application[];
    this.daemons = apps
      .filter((a) => a.name === SKYCOIN_DAEMON_PREFIX || a.name.startsWith(SKYCOIN_DAEMON_PREFIX + '-'))
      .sort((a, b) => a.name.localeCompare(b.name));
    // The skycoin-web app entry, when configured — drives the OPTIONAL
    // header controls (start/stop the standalone server). It no longer
    // gates the wallet.
    this.webApp = apps.find((a) => a.name === 'skycoin-web') || null;

    // Always iframe the same-origin, hypervisor-served wallet. Same URL on
    // the native and wasm visors → symmetric, and remote-safe (no
    // cross-host :8002). The HV serves /wallet/ (static bundle + node API
    // proxied over dmsg); no skycoin-web port is required.
    const url = '/wallet/';
    if (this.boundPk !== this.node.localPk || !this.iframeUrl) {
      this.iframeUrl = this.sanitizer.bypassSecurityTrustResourceUrl(url);
      this.boundPk = this.node.localPk;
    }
    this.fullWindowUrl = url;
    this.state = 'running';
  }

  openFullWindow() {
    if (!this.fullWindowUrl) {
 return; 
}
    window.open(this.fullWindowUrl, '_blank', 'noopener noreferrer');
  }

  // ---- skycoin-web app controls (start/stop + settings) ----

  isWebRunning(): boolean {
 return !!this.webApp && this.webApp.status === 1; 
}
  isWebStarting(): boolean {
 return !!this.webApp && this.webApp.status === 3; 
}

  webStatusKey(): string {
    if (!this.webApp) {
 return 'wallet.daemons.status.unknown'; 
}
    switch (this.webApp.status) {
      case 0: return 'wallet.daemons.status.stopped';
      case 1: return 'wallet.daemons.status.running';
      case 2: return 'wallet.daemons.status.errored';
      case 3: return 'wallet.daemons.status.starting';
      default: return 'wallet.daemons.status.unknown';
    }
  }

  toggleWebApp() {
    if (!this.node || !this.webApp || this.webAppBusy) {
 return; 
}
    const start = !this.isWebRunning();
    const name = this.webApp.name;
    this.webAppBusy = true;
    this.appsService.changeAppState(this.node.localPk, name, start).subscribe({
      next: () => {
 this.webAppBusy = false; 
},
      error: () => {
        this.webAppBusy = false;
        this.snackbar.showError(start ? 'wallet.daemons.start-error' : 'wallet.daemons.stop-error');
      },
    });
  }

  toggleWebSettings() {
 this.webAppSettingsOpen = !this.webAppSettingsOpen; 
}
  onWebSettingsSaved() {
 this.webAppSettingsOpen = false; 
}

  // True iff there are running skycoin-daemon* instances whose ports
  // could be added to skycoin-web's --node-url list. Drives the
  // "Use local daemons" button's visibility / disabled state.
  get hasRunningDaemons(): boolean {
    return this.daemons.some((d) => d.status === 1);
  }

  /** Replaces skycoin-web's --node-url args with the http endpoints
   *  of the currently-running skycoin-daemon* instances on this
   *  visor. Other args are preserved verbatim. The wallet must be
   *  restarted (Stop, then Start) for it to pick up the new node
   *  list — skycoin-web reads --node-url at startup. */
  applyLocalDaemons() {
    if (!this.node || !this.webApp) {
 return; 
}
    const running = this.daemons.filter((d) => d.status === 1);
    if (running.length === 0) {
      this.snackbar.showError('wallet.daemons.no-running');

      return;
    }
    const nodeUrls = running.map((d) => {
      const port = parseDaemonPort((d.args as string[]) || []);

      return `http://127.0.0.1:${port}`;
    });

    const args = stripFlag((this.webApp.args as string[]) || [], '--node-url');
    for (const url of nodeUrls) {
      args.push('--node-url', url);
    }
    const body = { args: shellJoin(args) };
    this.appsService.setAppFullConfig(this.node.localPk, this.webApp.name, body).subscribe({
      next: () => {
        this.snackbar.showDone('wallet.daemons.local-applied');
      },
      error: (e: any) => {
        const msg = (e && e.message) ? e.message : 'Failed to update node-url args';
        this.snackbar.showError(msg);
      },
    });
  }

  // ---- Skycoin daemon multi-instance controls ----

  isDaemonRunning(d: Application): boolean {
 return d.status === 1; 
}
  isDaemonStarting(d: Application): boolean {
 return d.status === 3; 
}

  daemonStatusKey(d: Application): string {
    switch (d.status) {
      case 0: return 'wallet.daemons.status.stopped';
      case 1: return 'wallet.daemons.status.running';
      case 2: return 'wallet.daemons.status.errored';
      case 3: return 'wallet.daemons.status.starting';
      default: return 'wallet.daemons.status.unknown';
    }
  }

  toggleDaemon(d: Application) {
    if (!this.node || this.daemonsBusy.has(d.name)) {
 return; 
}
    const start = !this.isDaemonRunning(d);
    const name = d.name;
    this.daemonsBusy.add(name);
    this.appsService.changeAppState(this.node.localPk, name, start).subscribe({
      next: () => {
        this.daemonsBusy.delete(name);
        this.snackbar.showDone(start ? 'wallet.daemons.started' : 'wallet.daemons.stopped');
      },
      error: () => {
        this.daemonsBusy.delete(name);
        this.snackbar.showError(start ? 'wallet.daemons.start-error' : 'wallet.daemons.stop-error');
      },
    });
  }

  toggleDaemonSettings(name: string) {
    if (this.expandedDaemons.has(name)) {
      this.expandedDaemons.delete(name);
    } else {
      this.expandedDaemons.add(name);
    }
  }

  isDaemonSettingsOpen(name: string): boolean {
    return this.expandedDaemons.has(name);
  }

  onDaemonSettingsSaved(name: string) {
    this.expandedDaemons.delete(name);
  }

  addDaemon() {
    if (!this.node) {
 return; 
}
    let suggested = SKYCOIN_DAEMON_PREFIX;
    if (this.daemons.some((d) => d.name === SKYCOIN_DAEMON_PREFIX)) {
      const used = new Set(this.daemons.map((d) => d.name));
      let n = 2;
      while (used.has(`${SKYCOIN_DAEMON_PREFIX}-${n}`)) {
 n++; 
}
      suggested = `${SKYCOIN_DAEMON_PREFIX}-${n}`;
    }
     
    const name = (window.prompt('Daemon instance name (one per fiberchain):', suggested) || '').trim();
    if (!name) {
 return; 
}
    if (this.daemonsBusy.has('add')) {
 return; 
}
    this.daemonsBusy.add('add');
    this.appsService.addApp(this.node.localPk, name, SKYCOIN_DAEMON_PREFIX).subscribe({
      next: (app: Application) => {
        this.daemonsBusy.delete('add');
        this.snackbar.showDone('wallet.daemons.added');
        // Auto-expand the new instance's settings panel so the
        // operator can immediately set FIBER_TOML / API set /
        // data dir before starting. The next NodeComponent poll
        // will fold the new app into this.daemons via recompute().
        this.expandedDaemons.add(app.name);
      },
      error: () => {
        this.daemonsBusy.delete('add');
        this.snackbar.showError('wallet.daemons.add-error');
      },
    });
  }
}

/** Reads the --port value out of a skycoin-daemon's args slice.
 *  Accepts both `--port 6420` and `--port=6420`. Defaults to 6420
 *  if the flag isn't set (skycoin's compile-time default). */
function parseDaemonPort(args: string[]): number {
  for (let i = 0; i < args.length; i++) {
    const a = args[i];
    if (a === '--port' || a === '-p') {
      if (i + 1 < args.length) {
        const n = parseInt(args[i + 1], 10);
        if (!isNaN(n)) {
 return n; 
}
      }
    } else if (a.startsWith('--port=')) {
      const n = parseInt(a.substring('--port='.length), 10);
      if (!isNaN(n)) {
 return n; 
}
    }
  }

  return 6420;
}

/** Strips every occurrence of a flag and its value from the args.
 *  Handles both two-arg `--flag value` and equals `--flag=value`
 *  forms. Used by applyLocalDaemons to wipe the existing
 *  --node-url entries before re-emitting them. */
function stripFlag(args: string[], flag: string): string[] {
  const eq = flag + '=';
  const out: string[] = [];
  for (let i = 0; i < args.length; i++) {
    const a = args[i];
    if (a === flag) {
      i++; // skip value
      continue;
    }
    if (a.startsWith(eq)) {
      continue;
    }
    out.push(a);
  }

  return out;
}

/** Joins string args back into a shell-like string for the PUT body.
 *  Tokens with whitespace get wrapped in double quotes. Mirrors the
 *  same logic the universal panel uses on save so round-tripping
 *  with visorconfig.SplitArgs is consistent. */
function shellJoin(args: string[]): string {
  return args.map(a => /[\s"']/.test(a) ? `"${a.replace(/"/g, '\\"')}"` : a).join(' ');
}

