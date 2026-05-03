import { Component, OnDestroy, OnInit } from '@angular/core';
import { Subscription, interval, startWith } from 'rxjs';
import { switchMap } from 'rxjs/operators';

import {
  DmsgClientSessions,
  DmsgClientSessionInfo,
  DmsgConnectAllResult,
  DmsgSettingsService,
} from 'src/app/services/dmsg-settings.service';
import { PageBaseComponent } from 'src/app/utils/page-base';
import { SnackbarService } from 'src/app/services/snackbar.service';
import { NodeComponent } from '../node/node.component';
import { Node } from '../../../app.datatypes';

/**
 * Per-visor DMSG tab content. Shows the dmsg server session list
 * for each of the visor's three independent dmsg clients (main,
 * embedded route setup-node, embedded transport setup-node) and
 * exposes "Connect to all servers" + "Set sessions count" actions.
 *
 * Was a top-level page (/nodes/dmsg-settings) showing the local
 * visor only — it's a per-visor tab now so the controls operate on
 * whichever visor the user is looking at, including remote visors.
 */
@Component({
  selector: 'app-dmsg-settings',
  templateUrl: './dmsg-settings.component.html',
  styleUrls: ['./dmsg-settings.component.scss'],
  standalone: false,
})
export class DmsgSettingsComponent extends PageBaseComponent implements OnInit, OnDestroy {
  pk = '';
  sessions: DmsgClientSessions | null = null;
  loading = true;
  error: string | null = null;
  lastUpdated: Date | null = null;

  // Form model for the sessions-count input.
  sessionsCountInput = 0;

  // In-flight action state — prevents double-submit and shows a spinner.
  connectAllInFlight = false;
  setCountInFlight = false;

  // Most recent action result for summary display under the buttons.
  lastActionResult: DmsgConnectAllResult | null = null;
  lastActionLabel = '';

  private nodeSub: Subscription;
  private pollSub: Subscription;

  constructor(
    private dmsgSvc: DmsgSettingsService,
    private snackbar: SnackbarService,
  ) {
    super();
  }

  ngOnInit() {
    this.nodeSub = NodeComponent.currentNode.subscribe((node: Node) => {
      const wasUnset = !this.pk;
      this.pk = node?.localPk || '';
      if (wasUnset && this.pk) {
        this.startPolling();
      }
    });
    return super.ngOnInit();
  }

  ngOnDestroy(): void {
    this.nodeSub?.unsubscribe();
    this.pollSub?.unsubscribe();
  }

  /** Poll every 20s. 20s (not 15s like services-health) because the
   *  sessions list changes rarely and we want to keep RPC traffic to
   *  the visor low. */
  private startPolling() {
    this.pollSub = interval(20000)
      .pipe(
        startWith(0),
        switchMap(() => this.dmsgSvc.getSessions(this.pk)),
      )
      .subscribe({
        next: (sessions) => {
          this.sessions = sessions || {};
          this.loading = false;
          this.error = null;
          this.lastUpdated = new Date();
        },
        error: (err) => {
          this.loading = false;
          this.error = err?.message || 'Failed to fetch dmsg sessions';
        },
      });
  }

  refresh(): void {
    if (!this.pk) { return; }
    this.dmsgSvc.getSessions(this.pk).subscribe({
      next: (sessions) => {
        this.sessions = sessions || {};
        this.lastUpdated = new Date();
      },
      error: () => { /* next poll surfaces errors */ },
    });
  }

  connectAll(): void {
    if (this.connectAllInFlight || !this.pk) { return; }
    this.connectAllInFlight = true;
    this.lastActionResult = null;
    this.dmsgSvc.connectAll(this.pk).subscribe({
      next: (result) => {
        this.connectAllInFlight = false;
        this.lastActionResult = result;
        this.lastActionLabel = 'Connect to all';
        this.snackbar.showDone(
          `Opened ${result.newly_connected} new session(s); already had ${result.already_connected}.`,
        );
        this.refresh();
      },
      error: (err) => {
        this.connectAllInFlight = false;
        this.snackbar.showError(`connect-all failed: ${err?.message || 'unknown error'}`);
      },
    });
  }

  applySessionsCount(): void {
    if (this.setCountInFlight || !this.pk) { return; }
    if (this.sessionsCountInput < 0) {
      this.snackbar.showError('Sessions count must be >= 0');
      return;
    }
    this.setCountInFlight = true;
    this.lastActionResult = null;
    this.dmsgSvc.setSessionsCount(this.pk, this.sessionsCountInput).subscribe({
      next: (result) => {
        this.setCountInFlight = false;
        this.lastActionResult = result;
        this.lastActionLabel = `Set sessions_count = ${this.sessionsCountInput}`;
        this.snackbar.showDone(
          `Persisted sessions_count=${this.sessionsCountInput}; opened ${result.newly_connected} new session(s).`,
        );
        this.refresh();
      },
      error: (err) => {
        this.setCountInFlight = false;
        this.snackbar.showError(`set-sessions failed: ${err?.message || 'unknown error'}`);
      },
    });
  }

  clientList(): DmsgClientSessionInfo[] {
    const out: DmsgClientSessionInfo[] = [];
    if (!this.sessions) { return out; }
    if (this.sessions.main) { out.push(this.sessions.main); }
    if (this.sessions.route_setup) { out.push(this.sessions.route_setup); }
    if (this.sessions.transport_setup) { out.push(this.sessions.transport_setup); }
    return out;
  }

  roleLabel(role: string): string {
    switch (role) {
      case 'main': return 'Main visor';
      case 'route_setup': return 'Route Setup Node';
      case 'transport_setup': return 'Transport Setup Node';
      default: return role;
    }
  }

  trackByRole(_: number, c: DmsgClientSessionInfo): string { return c.role; }
  trackByPk(_: number, pk: string): string { return pk; }

  objectKeys(o: { [k: string]: any } | undefined | null): string[] {
    return o ? Object.keys(o) : [];
  }
}
