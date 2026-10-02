import { Component, OnDestroy, OnInit, ChangeDetectorRef, ChangeDetectionStrategy } from '@angular/core';
import { Subscription, interval, startWith } from 'rxjs';
import { switchMap, catchError } from 'rxjs/operators';
import { of } from 'rxjs';

import { TabButtonData } from '../../layout/top-bar/top-bar.component';
import { PageBaseComponent } from 'src/app/utils/page-base';
import { ApiService } from 'src/app/services/api.service';
import { homeTabsData } from 'src/app/utils/home-tabs';

/** Mirrors pkg/deployment/tpd/store.TransportLatency. */
interface TransportLatency {
  min: number; // µs
  max: number;
  avg: number;
}

/**
 * Mirrors compactTransportRow in pkg/visor/hypervisor_handlers_tpd_reduce.go.
 *
 * The daily bandwidth array is folded to sent/recv by the hypervisor now. It
 * used to arrive raw: on the production mesh that is ~180k records and 60MB+
 * of JSON, which the upstream truncated, the write deadline killed, and this
 * table then tried to render in one pass.
 */
interface TransportMetric {
  id: string;
  type: string;
  live: boolean;
  edges?: string[];
  latency?: TransportLatency;
  sent: number;
  recv: number;
}

/** Mirrors compactTransportMetrics in the same file. */
interface NetworkTransportsResponse {
  metrics: TransportMetric[];
  total: number;
  returned: number;
  live: number;
  network_bandwidth: number;
  partial: boolean;
}

/** Compact "by transport" row: one per TPD transport. */
interface ByTransportRow {
  id: string;
  type: string;
  edge_a: string;
  edge_b: string;
  sent: number;
  recv: number;
  bandwidth: number;
  latency?: TransportLatency;
  live: boolean; // TPD's per-transport liveness flag
}

/** Tree node: one visor with its transports as children. */
interface VisorNode {
  pk: string;
  sent: number;
  recv: number;
  bandwidth: number;
  transports: VisorChildTp[];
  liveCount: number;   // # of children with live=true
  offlineCount: number;
  expanded: boolean;
}
interface VisorChildTp {
  id: string;
  type: string;
  remote: string;
  sent: number;
  recv: number;
  bandwidth: number;
  latency?: TransportLatency;
  live: boolean;
}

type ViewMode = 'compact' | 'tree';

/**
 * Network-wide Transports view, fed by TPD's /metrics endpoint
 * (proxied through the local visor's DmsgHTTP). Two render modes:
 *   - "compact": one row per transport id (mirrors `cli tp metrics -tv`)
 *   - "tree": visors as parents with their transports as children
 *     (mirrors `cli tp metrics --tree`)
 */
@Component({
  selector: 'app-network-transports',
  templateUrl: './network-transports.component.html',
  styleUrls: ['./network-transports.component.scss'],
  standalone: false,
  changeDetection: ChangeDetectionStrategy.OnPush
})
export class NetworkTransportsComponent extends PageBaseComponent implements OnInit, OnDestroy {
  tabsData: TabButtonData[] = [];
  loading = true;
  error: string | null = null;
  lastUpdated: Date | null = null;
  days = 1;
  viewMode: ViewMode = 'compact';
  // Compact-view edge columns are wide (66-char PKs ×2). Hide them
  // when the operator only cares about ID/type/bandwidth/latency.
  hideEdges = false;
  // Hide offline transports — when false, dim them in place; when
  // true, drop them from the rendered view entirely. The TPD live
  // flag is per-transport, not per-edge.
  hideOffline = false;

  rawCount = 0;
  // Rows the hypervisor kept after the cap, and whether the upstream body was
  // cut off before it finished. Both are shown, because a table that silently
  // displays 2000 of 180000 transports is worse than one that says so.
  returnedCount = 0;
  liveTotal = 0;
  // The metrics feed is a CXO subscription; on a cold start it needs a moment
  // to fill. That is a wait, not an error.
  syncing = false;
  partial = false;
  networkBandwidth = 0;
  byTransport: ByTransportRow[] = [];
  byVisor: VisorNode[] = [];

  private sub!: Subscription;

  constructor(private api: ApiService, private cdr: ChangeDetectorRef) {
    super();
    this.tabsData = homeTabsData();
  }

  override ngOnInit() {
    // 5min cadence: TPD metrics roll up daily, no benefit in
    // anything tighter. The Refresh button below the table forces
    // a fresh fetch when the user wants a current sample.
    this.sub = interval(300000)
      .pipe(
        startWith(0),
        switchMap(() => this.fetch()),
      )
      .subscribe();

    return super.ngOnInit();
  }

  ngOnDestroy(): void {
    this.sub?.unsubscribe();
  }

  refreshNow() {
 this.fetch().subscribe(); 
}

  setDays(d: number) {
    if (d === this.days) {
 return; 
}
    this.days = d;
    this.fetch().subscribe();
  }

  setViewMode(m: ViewMode) {
    this.viewMode = m;
  }

  setHideEdges(hide: boolean) {
    this.hideEdges = hide;
  }

  setHideOffline(hide: boolean) {
    if (hide === this.hideOffline) {
      return;
    }
    this.hideOffline = hide;
    // Refetch rather than filtering what we already have. The hypervisor caps
    // the response, and dead transports carry most of the historical bandwidth
    // on a real mesh, so filtering the capped rows locally would leave almost
    // nothing. Asking the server for live-only gets the top N that are live.
    this.fetch().subscribe();
  }

  /** Compact-view rows after applying the offline filter. */
  get visibleByTransport(): ByTransportRow[] {
    return this.hideOffline ? this.byTransport.filter((r) => r.live) : this.byTransport;
  }

  /** Tree-view visors after applying the offline filter (drops
   *  visors whose every transport is offline; surviving visors keep
   *  only their live children). */
  get visibleByVisor(): VisorNode[] {
    if (!this.hideOffline) {
 return this.byVisor; 
}

    return this.byVisor
      .map((v) => {
return {
        ...v,
        transports: v.transports.filter((c) => c.live),
      }
})
      .filter((v) => v.transports.length > 0);
  }

  toggleVisor(v: VisorNode) {
 v.expanded = !v.expanded; 
}

  private fetch() {
    this.loading = this.byTransport.length === 0 && this.byVisor.length === 0;

    return this.api.get(
      `network/transports?days=${this.days}` + (this.hideOffline ? '&live=true' : '')
    ).pipe(
      catchError((err) => {
        // 503 means the CXO metrics feed is still syncing — not a failure, and
        // it clears on its own. Saying "failed" there sends people looking for
        // a fault that isn't present.
        this.syncing = err?.status === 503;
        this.error = this.syncing
          ? null
          : err?.message || 'Failed to fetch transports';
        this.loading = false;
        this.cdr.markForCheck();

        return of(null);
      }),
      switchMap((rows) => {
        if (rows === null) {
 return of(null);
}
        this.consume(rows as NetworkTransportsResponse);

        return of(rows);
      }),
    );
  }

  private consume(resp: NetworkTransportsResponse) {
    // Defensive: an older hypervisor still returns a bare array.
    const metrics: TransportMetric[] = Array.isArray(resp)
      ? (resp as TransportMetric[])
      : Array.isArray(resp?.metrics) ? resp.metrics : [];

    this.rawCount = Array.isArray(resp) ? metrics.length : (resp?.total ?? metrics.length);
    this.returnedCount = Array.isArray(resp) ? metrics.length : (resp?.returned ?? metrics.length);
    this.liveTotal = Array.isArray(resp) ? 0 : (resp?.live ?? 0);
    this.partial = Array.isArray(resp) ? false : !!resp?.partial;

    let networkBw = Array.isArray(resp) ? 0 : (resp?.network_bandwidth ?? 0);
    const byTp: ByTransportRow[] = [];
    const byVisorMap = new Map<string, VisorNode>();

    for (const m of metrics) {
      if (!m.edges || m.edges.length < 2) {
 continue;
}
      const aToB = m.sent || 0;
      const bToA = m.recv || 0;
      const bw = aToB + bToA;
      if (Array.isArray(resp)) {
        // Legacy bare-array response: no server-side total to take.
        networkBw += bw;
      }

      byTp.push({
        id: m.id,
        type: m.type,
        edge_a: m.edges[0],
        edge_b: m.edges[1],
        sent: aToB,
        recv: bToA,
        bandwidth: bw,
        latency: m.latency,
        live: !!m.live,
      });

      // Edge A perspective.
      const a = byVisorMap.get(m.edges[0]) || this.newVisorNode(m.edges[0]);
      a.sent += aToB; a.recv += bToA; a.bandwidth += bw;
      if (m.live) {
 a.liveCount++; 
} else {
 a.offlineCount++; 
}
      a.transports.push({
        id: m.id, type: m.type, remote: m.edges[1],
        sent: aToB, recv: bToA, bandwidth: bw, latency: m.latency,
        live: !!m.live,
      });
      byVisorMap.set(m.edges[0], a);

      // Edge B perspective.
      const b = byVisorMap.get(m.edges[1]) || this.newVisorNode(m.edges[1]);
      b.sent += bToA; b.recv += aToB; b.bandwidth += bw;
      if (m.live) {
 b.liveCount++; 
} else {
 b.offlineCount++; 
}
      b.transports.push({
        id: m.id, type: m.type, remote: m.edges[0],
        sent: bToA, recv: aToB, bandwidth: bw, latency: m.latency,
        live: !!m.live,
      });
      byVisorMap.set(m.edges[1], b);
    }

    byTp.sort((x, y) => y.bandwidth - x.bandwidth);
    const visors = Array.from(byVisorMap.values()).sort((x, y) => y.bandwidth - x.bandwidth);
    visors.forEach((v) => v.transports.sort((x, y) => y.bandwidth - x.bandwidth));

    this.byTransport = byTp;
    this.byVisor = visors;
    this.networkBandwidth = networkBw;
    this.loading = false;
    this.error = null;
    this.syncing = false;
    this.lastUpdated = new Date();
    this.cdr.markForCheck();
  }

  private newVisorNode(pk: string): VisorNode {
    return { pk: pk, sent: 0, recv: 0, bandwidth: 0, transports: [], liveCount: 0, offlineCount: 0, expanded: false };
  }


  /** Bytes → human readable (KiB/MiB/GiB). */
  fmtBytes(b: number): string {
    if (!b || b < 0) {
 return '-'; 
}
    const u = ['B', 'KB', 'MB', 'GB', 'TB'];
    let i = 0, v = b;
    while (v >= 1024 && i < u.length - 1) {
 v /= 1024; i++; 
}

    return v < 10 ? v.toFixed(1) + ' ' + u[i] : Math.round(v) + ' ' + u[i];
  }

  /** Latency µs → "X.Xms" or "Yμs". */
  fmtLatency(l?: TransportLatency): string {
    if (!l || !l.avg) {
 return '-'; 
}
    const ms = l.avg / 1000;
    if (ms < 1) {
 return Math.round(l.avg) + 'μs'; 
}
    if (ms < 1000) {
 return ms.toFixed(1) + 'ms'; 
}

    return (ms / 1000).toFixed(2) + 's';
  }

  fmtLatencyFull(l?: TransportLatency): string {
    if (!l || !l.avg) {
 return '-'; 
}

    return (l.min / 1000).toFixed(1) + ' / ' + (l.avg / 1000).toFixed(1) + ' / ' + (l.max / 1000).toFixed(1) + ' ms';
  }

  trackTpId(_: number, e: ByTransportRow): string {
 return e.id; 
}
  trackVisorPk(_: number, n: VisorNode): string {
 return n.pk; 
}
  trackChildId(_: number, c: VisorChildTp): string {
 return c.id; 
}
}
