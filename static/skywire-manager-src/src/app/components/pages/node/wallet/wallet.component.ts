import { Component, OnDestroy, OnInit, ChangeDetectionStrategy, ChangeDetectorRef } from '@angular/core';
import { Subscription } from 'rxjs';
import { DomSanitizer, SafeResourceUrl } from '@angular/platform-browser';

import { Node, Application } from '../../../../app.datatypes';
import { Router } from '@angular/router';

import { NodeComponent } from '../node.component';
import { PageBaseComponent } from 'src/app/utils/page-base';
import { AppsService } from 'src/app/services/apps.service';
import { SnackbarService } from 'src/app/services/snackbar.service';

const SKYCOIN_DAEMON_PREFIX = 'skycoin-daemon';

/**
 * Per-visor Wallet tab. The wallet itself is the dashboard's #/wallet page
 * (src/app/wallet): skycoin-web compiled into the dashboard, its node queries
 * proxied by the hypervisor over the visor's dmsg client
 * (pkg/visor/hypervisor_handlers_wallet.go). Its wallets live in this
 * browser, so it is the same wallet whichever visor is selected; this tab
 * opens it, sets the coin node its queries go to, and manages the visor's
 * skycoin-daemon instances.
 *
 * The skycoin-web *app* (own port, disk wallets, server-side multi-coin)
 * remains an opt-in "power" mode: when it's configured on the visor the
 * header exposes start/stop + settings, but it does not gate the wallet.
 */
@Component({
  selector: 'app-wallet',
  templateUrl: './wallet.component.html',
  styleUrls: ['./wallet.component.scss'],
  standalone: false,
  changeDetection: ChangeDetectionStrategy.OnPush
})
export class WalletComponent extends PageBaseComponent implements OnInit, OnDestroy {
  node!: Node;
  // Possible UI states. The template branches on these.
  state: 'unknown' | 'not-configured' | 'not-running' | 'running' = 'unknown';

  // ---- coin-backend config ----
  // The config UI + its localStorage keys (skywire-wallet-mode, -coin-nodes,
  // -coin-node, -wallet-service, -btc-backend, -btc-proxy) live in the shared
  // wallet/config page (pkg/wasmhv/browseui/walletconfig.go), embedded below.
  // The wallet's backend interceptor (src/app/wallet) reads them per request
  // on both the native and wasm visors, so a change applies to its next query.
  backendOpen = false;
  // The shared wallet-config page, embedded as an iframe when the Node panel
  // is open. It is the SAME page + localStorage keys the ☰ wallet window uses,
  // so there is one config implementation. Built once the node is known: on a
  // wasm visor the browser can't egress to clearnet itself, so the page is
  // told (?wasm=1) to require the BTC skysocks exit. Relative, so it stays
  // under the desk's /vnet/<port>/ prefix.
  configUrl: SafeResourceUrl | null = null;

  // The skycoin-web app entry from this.node.apps, when present.
  // Drives the optional local-server controls (start/stop/settings).
  // Null when the app isn't configured on the visor at all.
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

  private nodeSub!: Subscription;

  constructor(
    private sanitizer: DomSanitizer,
    private appsService: AppsService,
    private snackbar: SnackbarService,
    private router: Router,
    private changeDetectorRef: ChangeDetectorRef,
  ) {
 super();
}

  override ngOnInit() {
    this.nodeSub = NodeComponent.currentNode.subscribe((node: Node) => {
      this.node = node;
      this.recompute();
      this.changeDetectorRef.markForCheck();
    });

    return super.ngOnInit();
  }

  ngOnDestroy(): void {
    this.nodeSub?.unsubscribe();
  }

  toggleBackend() {
    this.backendOpen = !this.backendOpen;
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

    // Built once: NodeComponent.currentNode emits on every polling refresh,
    // and a new SafeResourceUrl would reload the config page each time.
    if (!this.configUrl) {
      this.configUrl = this.sanitizer.bypassSecurityTrustResourceUrl('wallet/config' + (this.isWasm ? '?wasm=1' : ''));
    }
    this.state = 'running';
  }

  openWallet() {
    this.router.navigate(['/wallet']);
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

  /** A browser/wasm visor can't run skycoin-daemon or skycoin-web host
   *  processes, so the daemon-instance controls are hidden there — the
   *  wallet (client-side wallets, node proxied over dmsg) is all
   *  that applies. Mirrors node.component's wasm gating. */
  get isWasm(): boolean {
    return !!this.node && (this.node as any).arch === 'wasm';
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

