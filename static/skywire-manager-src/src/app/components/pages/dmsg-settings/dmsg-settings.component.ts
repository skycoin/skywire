import { Component, OnDestroy, OnInit } from '@angular/core';
import { Subscription, interval, startWith } from 'rxjs';
import { switchMap } from 'rxjs/operators';

import {
  DmsgClientSessions,
  DmsgClientSessionInfo,
  DmsgConnectAllResult,
  DmsgSettingsService,
} from 'src/app/services/dmsg-settings.service';
import { TabButtonData } from '../../layout/top-bar/top-bar.component';
import { PageBaseComponent } from 'src/app/utils/page-base';
import { SnackbarService } from 'src/app/services/snackbar.service';

/**
 * DMSG settings dashboard. Shows the dmsg server session list for each
 * of the visor's three independent dmsg clients (main, embedded route
 * setup-node, embedded transport setup-node) and exposes two actions:
 * "Connect to all servers" (one-shot) and "Set sessions count" (persist
 * + connect-all). Polled every 20s.
 */
@Component({
  selector: 'app-dmsg-settings',
  templateUrl: './dmsg-settings.component.html',
  styleUrls: ['./dmsg-settings.component.scss'],
  standalone: false,
})
export class DmsgSettingsComponent extends PageBaseComponent implements OnInit, OnDestroy {
  tabsData: TabButtonData[] = [];
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

  private sub: Subscription;

  constructor(
    private dmsgSvc: DmsgSettingsService,
    private snackbar: SnackbarService,
  ) {
    super();
    this.tabsData = [
      {
        icon: 'view_headline',
        label: 'nodes.title',
        linkParts: ['/nodes'],
      },
      {
        icon: 'monetization_on',
        label: 'nodes.rewards-title',
        linkParts: ['/nodes', 'rewards'],
      },
      {
        icon: 'health_and_safety',
        label: 'nodes.services-health-title',
        linkParts: ['/nodes', 'services-health'],
      },
      {
        icon: 'hub',
        label: 'nodes.dmsg-settings-title',
        linkParts: ['/nodes', 'dmsg-settings'],
      },
      {
        icon: 'bubble_chart',
        label: 'node.details.tpviz.title',
        linkParts: [],
        externalUrl: '/tp-viz/',
      },
      {
        icon: 'settings',
        label: 'settings.title',
        linkParts: ['/settings'],
      },
    ];
  }

  ngOnInit() {
    // Poll every 20s. 20s (not 15s like services-health) because the
    // sessions list changes rarely and we want to keep RPC traffic to
    // the visor low.
    this.sub = interval(20000)
      .pipe(
        startWith(0),
        switchMap(() => this.dmsgSvc.getSessions()),
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
    return super.ngOnInit();
  }

  ngOnDestroy(): void {
    if (this.sub) {
      this.sub.unsubscribe();
    }
  }

  /** Refresh the session list immediately, bypassing the poll interval. */
  refresh(): void {
    this.dmsgSvc.getSessions().subscribe({
      next: (sessions) => {
        this.sessions = sessions || {};
        this.lastUpdated = new Date();
      },
      error: () => {
        /* swallow — the next poll will surface errors */
      },
    });
  }

  /** One-shot: open a dmsg session to every known server, no persistence. */
  connectAll(): void {
    if (this.connectAllInFlight) {
      return;
    }
    this.connectAllInFlight = true;
    this.lastActionResult = null;
    this.dmsgSvc.connectAll().subscribe({
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

  /** Persist sessions_count and trigger connect-all. */
  applySessionsCount(): void {
    if (this.setCountInFlight) {
      return;
    }
    if (this.sessionsCountInput < 0) {
      this.snackbar.showError('Sessions count must be >= 0');
      return;
    }
    this.setCountInFlight = true;
    this.lastActionResult = null;
    this.dmsgSvc.setSessionsCount(this.sessionsCountInput).subscribe({
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

  /** Ordered list of the clients present for ngFor iteration. */
  clientList(): DmsgClientSessionInfo[] {
    const out: DmsgClientSessionInfo[] = [];
    if (!this.sessions) {
      return out;
    }
    if (this.sessions.main) {
      out.push(this.sessions.main);
    }
    if (this.sessions.route_setup) {
      out.push(this.sessions.route_setup);
    }
    if (this.sessions.transport_setup) {
      out.push(this.sessions.transport_setup);
    }
    return out;
  }

  /** Human-readable label for the role. */
  roleLabel(role: string): string {
    switch (role) {
      case 'main':
        return 'Main visor';
      case 'route_setup':
        return 'Route Setup Node';
      case 'transport_setup':
        return 'Transport Setup Node';
      default:
        return role;
    }
  }

  trackByRole(_: number, c: DmsgClientSessionInfo): string {
    return c.role;
  }

  trackByPk(_: number, pk: string): string {
    return pk;
  }

  /** Keys of a map for mobile iteration. */
  objectKeys(o: { [k: string]: any } | undefined | null): string[] {
    return o ? Object.keys(o) : [];
  }
}
