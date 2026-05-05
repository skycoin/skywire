import { Component, OnDestroy, OnInit, ChangeDetectorRef } from '@angular/core';
import { Subscription } from 'rxjs';
import { MatDialog } from '@angular/material/dialog';

import { Node, Application } from '../../../../app.datatypes';
import { NodeComponent } from '../node.component';
import { PageBaseComponent } from 'src/app/utils/page-base';
import { AppsService } from 'src/app/services/apps.service';
import { SnackbarService } from 'src/app/services/snackbar.service';
import { SkysocksSettingsComponent } from '../apps/node-apps/skysocks-settings/skysocks-settings.component';
import { SkysocksClientSettingsComponent } from '../apps/node-apps/skysocks-client-settings/skysocks-client-settings.component';

const VPN_CLIENT = 'vpn-client';
const VPN_SERVER = 'vpn-server';

/**
 * Per-visor VPN tab. Surfaces start/stop + settings for the
 * vpn-client and vpn-server apps without forcing the operator
 * through the Apps list. The standalone /vpn/<pk>/... UX
 * (server picker, history, etc.) is still linked from here.
 */
@Component({
  selector: 'app-vpn',
  templateUrl: './vpn.component.html',
  styleUrls: ['./vpn.component.scss'],
  standalone: false,
})
export class VpnComponent extends PageBaseComponent implements OnInit, OnDestroy {
  node: Node;
  client: Application | null = null;
  server: Application | null = null;
  busy = new Set<string>();

  private nodeSub: Subscription;

  constructor(
    private appsService: AppsService,
    private snackbar: SnackbarService,
    private dialog: MatDialog,
    private cdr: ChangeDetectorRef,
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

  private recompute() {
    const apps = (this.node?.apps || []) as Application[];
    this.client = apps.find((a) => a.name === VPN_CLIENT) || null;
    this.server = apps.find((a) => a.name === VPN_SERVER) || null;
    this.cdr.markForCheck();
  }

  isRunning(app: Application | null): boolean {
    return !!app && app.status === 1;
  }

  isStarting(app: Application | null): boolean {
    return !!app && app.status === 3;
  }

  statusKey(app: Application | null): string {
    if (!app) { return 'vpn-tab.status.not-configured'; }
    switch (app.status) {
      case 0: return 'vpn-tab.status.stopped';
      case 1: return 'vpn-tab.status.running';
      case 2: return 'vpn-tab.status.errored';
      case 3: return 'vpn-tab.status.starting';
      default: return 'vpn-tab.status.unknown';
    }
  }

  toggle(app: Application | null, name: string) {
    if (!this.node || !app || this.busy.has(name)) { return; }
    const start = !this.isRunning(app);
    this.busy.add(name);
    this.appsService.changeAppState(this.node.localPk, name, start).subscribe({
      next: () => {
        this.busy.delete(name);
        this.snackbar.showDone(start ? 'vpn-tab.started' : 'vpn-tab.stopped');
      },
      error: () => {
        this.busy.delete(name);
        this.snackbar.showError(start ? 'vpn-tab.start-error' : 'vpn-tab.stop-error');
      },
    });
  }

  configureClient() {
    if (this.client) {
      SkysocksClientSettingsComponent.openDialog(this.dialog, this.client);
    }
  }

  configureServer() {
    if (this.server) {
      SkysocksSettingsComponent.openDialog(this.dialog, this.server);
    }
  }

  openStandaloneClient() {
    if (!this.node) { return; }
    window.open(location.origin + '/#/vpn/' + this.node.localPk + '/status', '_blank', 'noopener noreferrer');
  }
}
