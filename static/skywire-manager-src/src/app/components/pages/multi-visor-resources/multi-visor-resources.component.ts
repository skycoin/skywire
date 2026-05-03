import { Component, OnDestroy, OnInit, ChangeDetectorRef } from '@angular/core';
import { Subscription, interval, startWith, forkJoin, of } from 'rxjs';
import { switchMap, catchError } from 'rxjs/operators';

import { TabButtonData } from '../../layout/top-bar/top-bar.component';
import { PageBaseComponent } from 'src/app/utils/page-base';
import { homeTabsData } from 'src/app/utils/home-tabs';
import { NodeService } from 'src/app/services/node.service';
import { ApiService } from 'src/app/services/api.service';
import { Node } from 'src/app/app.datatypes';

/**
 * Resources tab on the home page — fans out one host-stats request
 * per connected visor and shows CPU/mem/disk/net at a glance for
 * the entire fleet. The per-visor Resources tab on the visor detail
 * page is the deep view (sparklines, runtime, network rates); this
 * is the bird's-eye summary so an operator can spot a misbehaving
 * visor without clicking into each one.
 *
 * Polling cadence is intentionally slower than the per-visor 1s —
 * each host-stats fetch is a separate visor RPC, so a 5s cadence
 * keeps the fan-out off the busy path even on a hypervisor with
 * many connected visors.
 */

interface HostProcess {
  pid?: number;
  cpu_percent?: number;
  mem_rss?: number;
  num_threads?: number;
}

interface HostStats {
  hostname?: string;
  cpu_percent?: number;
  cpu_count?: number;
  mem_total?: number;
  mem_used?: number;
  mem_percent?: number;
  disk_total?: number;
  disk_used?: number;
  disk_percent?: number;
  net_bytes_sent?: number;
  net_bytes_recv?: number;
  process?: HostProcess;
}

interface VisorRow {
  node: Node;
  stats?: HostStats;
  prevSent?: number;
  prevRecv?: number;
  txRate?: number; // bytes/sec
  rxRate?: number;
  lastSampleAt?: number;
  error?: string;
}

@Component({
  selector: 'app-multi-visor-resources',
  templateUrl: './multi-visor-resources.component.html',
  styleUrls: ['./multi-visor-resources.component.scss'],
  standalone: false,
})
export class MultiVisorResourcesComponent extends PageBaseComponent implements OnInit, OnDestroy {
  tabsData: TabButtonData[] = [];
  rows: VisorRow[] = [];
  loading = true;
  error: string | null = null;
  lastUpdated: Date | null = null;

  private sub: Subscription;

  constructor(
    private nodeService: NodeService,
    private api: ApiService,
    private cdr: ChangeDetectorRef,
  ) {
    super();
    this.tabsData = homeTabsData();
  }

  ngOnInit() {
    this.sub = interval(5000).pipe(
      startWith(0),
      switchMap(() => this.nodeService.getNodes()),
      switchMap((nodes: Node[]) => {
        const online = (nodes || []).filter((n) => n.online);
        if (online.length === 0) {
          return of({ nodes: nodes || [], stats: [] as ({ pk: string, stats?: HostStats, error?: string })[] });
        }
        // Fan out per-visor host-stats. catchError per-stream so a
        // single timeout doesn't kill the whole batch.
        const fetches = online.map((n) =>
          this.api.get(`visors/${n.localPk}/host-stats`).pipe(
            catchError((err) => of({ __error: err?.message || 'failed' })),
          ),
        );
        return forkJoin(fetches).pipe(
          switchMap((results: any[]) => of({
            nodes: nodes || [],
            stats: results.map((r, i) => ({
              pk: online[i].localPk,
              stats: r && !r.__error ? (r as HostStats) : undefined,
              error: r && r.__error ? r.__error : undefined,
            })),
          })),
        );
      }),
    ).subscribe({
      next: ({ nodes, stats }) => {
        this.mergeStats(nodes, stats);
        this.loading = false;
        this.error = null;
        this.lastUpdated = new Date();
        this.cdr.markForCheck();
      },
      error: (err) => {
        this.loading = false;
        this.error = err?.message || 'Failed to fetch resources';
      },
    });

    return super.ngOnInit();
  }

  ngOnDestroy(): void {
    this.sub?.unsubscribe();
  }

  private mergeStats(nodes: Node[], stats: { pk: string, stats?: HostStats, error?: string }[]) {
    const byPk = new Map<string, { stats?: HostStats, error?: string }>();
    stats.forEach((s) => byPk.set(s.pk, { stats: s.stats, error: s.error }));

    // Preserve previous samples to derive net rates.
    const prevByPk = new Map<string, VisorRow>();
    this.rows.forEach((r) => prevByPk.set(r.node.localPk, r));

    const now = Date.now();
    this.rows = nodes.map((node) => {
      const fresh = byPk.get(node.localPk);
      const prev = prevByPk.get(node.localPk);
      const row: VisorRow = { node, ...fresh };
      if (fresh?.stats) {
        const sent = fresh.stats.net_bytes_sent || 0;
        const recv = fresh.stats.net_bytes_recv || 0;
        if (prev?.lastSampleAt && prev.prevSent !== undefined && prev.prevRecv !== undefined) {
          const dt = (now - prev.lastSampleAt) / 1000;
          if (dt > 0) {
            const tx = Math.max(0, (sent - prev.prevSent) / dt);
            const rx = Math.max(0, (recv - prev.prevRecv) / dt);
            row.txRate = tx;
            row.rxRate = rx;
          }
        }
        row.prevSent = sent;
        row.prevRecv = recv;
        row.lastSampleAt = now;
      }
      return row;
    });
  }

  /** Color class buckets — same thresholds as per-visor monitor. */
  pctClass(pct?: number): string {
    if (pct === undefined || pct === null) { return 'pct-na'; }
    if (pct >= 90) { return 'pct-bad'; }
    if (pct >= 70) { return 'pct-warn'; }
    return 'pct-ok';
  }

  /** Bytes-per-second → human readable. */
  formatRate(bps?: number): string {
    if (bps === undefined || bps === null || bps < 0) { return '-'; }
    const u = ['B/s', 'KB/s', 'MB/s', 'GB/s'];
    let i = 0; let v = bps;
    while (v >= 1024 && i < u.length - 1) { v /= 1024; i++; }
    return v < 10 ? v.toFixed(1) + ' ' + u[i] : Math.round(v) + ' ' + u[i];
  }

  formatBytes(b?: number): string {
    if (b === undefined || b === null) { return '-'; }
    const u = ['B', 'KB', 'MB', 'GB', 'TB'];
    let i = 0; let v = b;
    while (v >= 1024 && i < u.length - 1) { v /= 1024; i++; }
    return v < 10 ? v.toFixed(1) + ' ' + u[i] : Math.round(v) + ' ' + u[i];
  }

  trackRow(_: number, r: VisorRow): string { return r.node.localPk; }
}
