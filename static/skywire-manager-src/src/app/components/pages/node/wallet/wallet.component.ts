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
 * Per-visor Wallet tab. Iframes the embedded `skywire skycoin web`
 * thin-client wallet — only meaningful when:
 *
 *   1. The skycoin-web app is configured for this visor (default-off
 *      out of config-gen; operator opts in via the Apps tab).
 *   2. The hypervisor UI's browser can reach the visor's local
 *      skycoin-web port. That works when the hypervisor and visor
 *      run on the same host (the common case — local hypervisor
 *      managing the local visor) because both share window.location's
 *      hostname. For remote visors the operator either points the
 *      browser at the visor's machine directly, or registers
 *      skycoin-web's port for skynet forwarding (`cli serve add`).
 *
 * The tab renders a clear "not configured" / "not running" / "running
 * but not iframable" message in the cases where we can't show the
 * wallet inline; only the working path actually iframes.
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
  resolvedHost = '';
  resolvedPort = 0;
  // Last node PK we built the iframe URL for. NodeComponent.currentNode
  // emits on every polling refresh; rebuilding the SafeResourceUrl on
  // every tick reloads the iframe and tears down whatever the wallet
  // had open (mirrors the bug we fixed on the terminal tab earlier).
  private boundPk = '';

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
  ) { super(); }

  ngOnInit() {
    this.nodeSub = NodeComponent.currentNode.subscribe((node: Node) => {
      this.node = node;
      this.recompute();
    });
    return super.ngOnInit();
  }

  ngOnDestroy(): void {
    this.nodeSub?.unsubscribe();
  }

  /** Re-evaluates the UI state from the latest node snapshot. Cheap;
   *  safe to call on every NodeComponent polling tick. */
  private recompute() {
    if (!this.node) { this.state = 'unknown'; return; }

    const apps = (this.node.apps || []) as Application[];
    this.daemons = apps
      .filter((a) => a.name === SKYCOIN_DAEMON_PREFIX || a.name.startsWith(SKYCOIN_DAEMON_PREFIX + '-'))
      .sort((a, b) => a.name.localeCompare(b.name));
    const app = apps.find((a) => a.name === 'skycoin-web');

    if (!app) {
      this.state = 'not-configured';
      this.iframeUrl = null;
      return;
    }

    // Status enum: 0=stopped, 1=running, 2=errored, 3=starting (per
    // appserver.AppStatus.String()). Anything other than running is
    // surfaced as "not running" so the user gets the same start-it
    // hint regardless of stopped/errored/starting.
    if (app.status !== 1) {
      this.state = 'not-running';
      this.iframeUrl = null;
      return;
    }

    const { host, port } = parseHostPort(app.args || []);
    this.resolvedHost = host;
    this.resolvedPort = port;

    // Construct the iframe URL using window.location.hostname for the
    // host part — that way local-hypervisor + local-visor "just works"
    // (same machine, same hostname). Remote visors will show a broken
    // iframe; the help text under the iframe explains why.
    const url = `http://${window.location.hostname}:${port}/`;
    if (this.boundPk !== this.node.localPk || !this.iframeUrl) {
      this.iframeUrl = this.sanitizer.bypassSecurityTrustResourceUrl(url);
      this.boundPk = this.node.localPk;
    }
    this.fullWindowUrl = url;
    this.state = 'running';
  }

  openFullWindow() {
    if (!this.fullWindowUrl) { return; }
    window.open(this.fullWindowUrl, '_blank', 'noopener noreferrer');
  }

  // ---- Skycoin daemon multi-instance controls ----

  isDaemonRunning(d: Application): boolean { return d.status === 1; }
  isDaemonStarting(d: Application): boolean { return d.status === 3; }

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
    if (!this.node || this.daemonsBusy.has(d.name)) { return; }
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
    if (!this.node) { return; }
    let suggested = SKYCOIN_DAEMON_PREFIX;
    if (this.daemons.some((d) => d.name === SKYCOIN_DAEMON_PREFIX)) {
      const used = new Set(this.daemons.map((d) => d.name));
      let n = 2;
      while (used.has(`${SKYCOIN_DAEMON_PREFIX}-${n}`)) { n++; }
      suggested = `${SKYCOIN_DAEMON_PREFIX}-${n}`;
    }
    // eslint-disable-next-line no-alert
    const name = (window.prompt('Daemon instance name (one per fiberchain):', suggested) || '').trim();
    if (!name) { return; }
    if (this.daemonsBusy.has('add')) { return; }
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

/** Picks --host and --port values out of the app args slice, with
 *  upstream skycoin-web defaults (127.0.0.1:8001) as the fallback.
 *  Accepts both `--host=value` and `--host value` forms. */
function parseHostPort(args: string[]): { host: string, port: number } {
  let host = '127.0.0.1';
  let port = 8001;
  for (let i = 0; i < args.length; i++) {
    const a = args[i];
    if (a === '--host' || a === '-H') {
      if (i + 1 < args.length) { host = args[i + 1]; }
    } else if (a.startsWith('--host=')) {
      host = a.substring('--host='.length);
    } else if (a === '--port' || a === '-p') {
      if (i + 1 < args.length) {
        const n = parseInt(args[i + 1], 10);
        if (!isNaN(n)) { port = n; }
      }
    } else if (a.startsWith('--port=')) {
      const n = parseInt(a.substring('--port='.length), 10);
      if (!isNaN(n)) { port = n; }
    }
  }
  return { host, port };
}
