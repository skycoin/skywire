import { Component, OnDestroy, OnInit, ChangeDetectorRef } from '@angular/core';
import { Subscription } from 'rxjs';

import { Node, Application } from '../../../../app.datatypes';
import { NodeComponent } from '../node.component';
import { PageBaseComponent } from 'src/app/utils/page-base';
import { AppsService } from 'src/app/services/apps.service';
import { SnackbarService } from 'src/app/services/snackbar.service';

const SKYSOCKS_SERVER = 'skysocks';
const SKYSOCKS_CLIENT_PREFIX = 'skysocks-client';

/**
 * Per-visor Skysocks tab. Surfaces start/stop + settings for the
 * skysocks (server) and skysocks-client apps. Lists every app
 * whose name starts with "skysocks-client" so multi-instance
 * setups (added via `cli skynet srv start --app=...`) appear
 * here too. Adding/removing instances from the UI is the
 * follow-up that needs hypervisor HTTP routes for AddApp /
 * DeleteApp / DoCustomSetting; this tab is read-only on that
 * dimension for now.
 */
@Component({
  selector: 'app-skysocks-tab',
  templateUrl: './skysocks.component.html',
  styleUrls: ['./skysocks.component.scss'],
  standalone: false,
})
export class SkysocksTabComponent extends PageBaseComponent implements OnInit, OnDestroy {
  node: Node;
  server: Application | null = null;
  clients: Application[] = [];
  busy = new Set<string>();
  // Names of apps whose universal settings panel is currently open
  // inline below their row.
  expandedSettings = new Set<string>();

  private nodeSub: Subscription;

  constructor(
    private appsService: AppsService,
    private snackbar: SnackbarService,
    private cdr: ChangeDetectorRef,
  ) { super(); }

  toggleSettings(name: string) {
    if (this.expandedSettings.has(name)) {
      this.expandedSettings.delete(name);
    } else {
      this.expandedSettings.add(name);
    }
  }
  isSettingsOpen(name: string): boolean { return this.expandedSettings.has(name); }
  onSettingsSaved(name: string) { this.expandedSettings.delete(name); }

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

  private recompute() {
    const apps = (this.node?.apps || []) as Application[];
    this.server = apps.find((a) => a.name === SKYSOCKS_SERVER) || null;
    this.clients = apps
      .filter((a) => a.name === SKYSOCKS_CLIENT_PREFIX || a.name.startsWith(SKYSOCKS_CLIENT_PREFIX + '-'))
      .sort((a, b) => a.name.localeCompare(b.name));
    this.cdr.markForCheck();
  }

  isRunning(app: Application | null): boolean {
    return !!app && app.status === 1;
  }

  isStarting(app: Application | null): boolean {
    return !!app && app.status === 3;
  }

  statusKey(app: Application | null): string {
    if (!app) { return 'skysocks-tab.status.not-configured'; }
    switch (app.status) {
      case 0: return 'skysocks-tab.status.stopped';
      case 1: return 'skysocks-tab.status.running';
      case 2: return 'skysocks-tab.status.errored';
      case 3: return 'skysocks-tab.status.starting';
      default: return 'skysocks-tab.status.unknown';
    }
  }

  toggle(app: Application | null) {
    if (!this.node || !app || this.busy.has(app.name)) { return; }
    const start = !this.isRunning(app);
    const name = app.name;
    this.busy.add(name);
    this.appsService.changeAppState(this.node.localPk, name, start).subscribe({
      next: () => {
        this.busy.delete(name);
        this.snackbar.showDone(start ? 'skysocks-tab.started' : 'skysocks-tab.stopped');
      },
      error: () => {
        this.busy.delete(name);
        this.snackbar.showError(start ? 'skysocks-tab.start-error' : 'skysocks-tab.stop-error');
      },
    });
  }

  addClient() {
    if (!this.node) { return; }
    // Suggest the next free skysocks-client-N name based on what
    // already exists; the operator can override in the prompt.
    let suggested = SKYSOCKS_CLIENT_PREFIX;
    if (this.clients.some((c) => c.name === SKYSOCKS_CLIENT_PREFIX)) {
      const used = new Set(this.clients.map((c) => c.name));
      let n = 2;
      while (used.has(`${SKYSOCKS_CLIENT_PREFIX}-${n}`)) { n++; }
      suggested = `${SKYSOCKS_CLIENT_PREFIX}-${n}`;
    }
    // eslint-disable-next-line no-alert
    const name = (window.prompt('Instance name (must be unique on this visor):', suggested) || '').trim();
    if (!name) { return; }
    if (this.busy.has('add')) { return; }
    this.busy.add('add');
    this.appsService.addApp(this.node.localPk, name, SKYSOCKS_CLIENT_PREFIX).subscribe({
      next: (app: Application) => {
        this.busy.delete('add');
        this.snackbar.showDone('skysocks-tab.added');
        // Auto-expand the new entry's settings panel so the
        // operator can fill in --srv (remote PK) before Start.
        this.expandedSettings.add(app.name);
      },
      error: () => {
        this.busy.delete('add');
        this.snackbar.showError('skysocks-tab.add-error');
      },
    });
  }
}
