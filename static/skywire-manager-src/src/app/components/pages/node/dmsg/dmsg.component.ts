import { Component, OnDestroy, OnInit, ChangeDetectorRef } from '@angular/core';
import { Subscription, interval, of, startWith } from 'rxjs';
import { switchMap, catchError } from 'rxjs/operators';

import { Node } from '../../../../app.datatypes';
import { NodeComponent } from '../node.component';
import { PageBaseComponent } from 'src/app/utils/page-base';
import { ApiService } from 'src/app/services/api.service';
import { SnackbarService } from 'src/app/services/snackbar.service';

/**
 * Per-visor DMSG diagnostics tab. Surfaces what `skywire cli dmsg
 * sessions` shows in the terminal: every dmsg client the visor is
 * running (main, transport_setup, sometimes more), each with its
 * connected dmsg-server PK list.
 *
 * Backend: GET /visors/<pk>/dmsg/sessions returns
 *   { clients: [
 *       { name: "main", pk: "...", connected_servers: ["...", ...] },
 *       { name: "transport_setup", pk: "...", connected_servers: [...] },
 *     ] }
 *
 * Why this is a separate tab: the Info tab already shows the main
 * client's connected DMSG servers, but visors run a second client
 * for transport_setup and any other role-scoped contexts (RSN, etc.)
 * with distinct PKs and distinct delegated-server sets. When a route
 * setup fails with "dmsg error 202 - cannot connect to delegated
 * server" the operator needs to see whether all roles are connected
 * to the right servers, not just the main client.
 */

interface DmsgClient {
  name: string;
  pk: string;
  connected_servers?: string[];
}

interface DmsgClientSessionsResponse {
  clients?: DmsgClient[];
}

@Component({
  selector: 'app-dmsg',
  templateUrl: './dmsg.component.html',
  styleUrls: ['./dmsg.component.scss'],
  standalone: false,
})
export class DmsgComponent extends PageBaseComponent implements OnInit, OnDestroy {
  node: Node;
  loading = true;
  error: string | null = null;
  clients: DmsgClient[] = [];
  fetchedAt: Date | null = null;
  connectAllInFlight = false;

  private nodeSub: Subscription;
  private pollSub: Subscription;
  private actionSub: Subscription;

  constructor(
    private api: ApiService,
    private snackbar: SnackbarService,
    private cdr: ChangeDetectorRef,
  ) { super(); }

  ngOnInit() {
    this.nodeSub = NodeComponent.currentNode.subscribe((node: Node) => {
      const wasUnset = !this.node;
      this.node = node;
      if (wasUnset && node) { this.startPolling(); }
    });
    return super.ngOnInit();
  }

  ngOnDestroy(): void {
    this.nodeSub?.unsubscribe();
    this.pollSub?.unsubscribe();
    this.actionSub?.unsubscribe();
  }

  refreshNow() {
    if (!this.node) { return; }
    this.fetchOnce().subscribe();
  }

  /**
   * Triggers /visors/<pk>/dmsg/connect-all which fans out and opens
   * a session to every dmsg-server in discovery. Useful when a
   * client is missing some servers and you want the visor to
   * eagerly establish the rest.
   */
  connectAll() {
    if (!this.node || this.connectAllInFlight) { return; }
    this.connectAllInFlight = true;
    this.actionSub?.unsubscribe();
    this.actionSub = this.api.post(
      `visors/${this.node.localPk}/dmsg/connect-all`, {},
    ).subscribe({
      next: () => {
        this.connectAllInFlight = false;
        this.snackbar.showDone('dmsg.connect-all-done');
        this.refreshNow();
      },
      error: (err) => {
        this.connectAllInFlight = false;
        this.snackbar.showError({ originalError: err, originalServerErrorMsg: err?.message } as any);
      },
    });
  }

  private startPolling() {
    // 5s feels live without spamming RPC. Sessions change on
    // dmsg-server churn — minutes, typically — so faster polling
    // wastes work.
    this.pollSub = interval(5000).pipe(
      startWith(0),
      switchMap(() => this.fetchOnce()),
    ).subscribe();
  }

  private fetchOnce() {
    return this.api.get(`visors/${this.node.localPk}/dmsg/sessions`).pipe(
      catchError((err) => {
        this.error = err?.message || 'Failed to fetch dmsg sessions';
        this.loading = false;
        this.cdr.markForCheck();
        return of(null);
      }),
      switchMap((resp: DmsgClientSessionsResponse | null) => {
        if (resp) {
          this.error = null;
          this.loading = false;
          this.clients = Array.isArray(resp.clients) ? resp.clients : [];
          this.fetchedAt = new Date();
          this.cdr.markForCheck();
        }
        return of(resp);
      }),
    );
  }

  trackClient(_idx: number, c: DmsgClient): string { return c.pk; }
  trackServer(_idx: number, pk: string): string { return pk; }
}
