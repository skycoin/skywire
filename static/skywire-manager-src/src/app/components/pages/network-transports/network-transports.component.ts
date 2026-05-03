import { Component, OnDestroy, OnInit, ChangeDetectorRef } from '@angular/core';
import { Subscription, interval, startWith } from 'rxjs';
import { switchMap, catchError } from 'rxjs/operators';
import { of } from 'rxjs';

import { TabButtonData } from '../../layout/top-bar/top-bar.component';
import { PageBaseComponent } from 'src/app/utils/page-base';
import { ApiService } from 'src/app/services/api.service';
import { homeTabsData } from 'src/app/utils/home-tabs';

/** Mirrors pkg/transport-discovery/store.TransportLatency. */
interface TransportLatency {
  min: number; // µs
  max: number;
  avg: number;
}

/** Mirrors pkg/transport-discovery/store.EdgeBandwidth. */
interface EdgeBandwidth { sent: number; recv: number; }
interface DailyEdgeBandwidth { date: string; a?: EdgeBandwidth; b?: EdgeBandwidth; }

/** Mirrors pkg/transport-discovery/store.TransportMetric. */
interface TransportMetric {
  id: string;
  type: string;
  live: boolean;
  edges?: string[];
  latency?: TransportLatency;
  daily: DailyEdgeBandwidth[];
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
}

/** Tree node: one visor with its transports as children. */
interface VisorNode {
  pk: string;
  sent: number;
  recv: number;
  bandwidth: number;
  transports: VisorChildTp[];
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

  rawCount = 0;
  networkBandwidth = 0;
  byTransport: ByTransportRow[] = [];
  byVisor: VisorNode[] = [];

  private sub: Subscription;

  constructor(private api: ApiService, private cdr: ChangeDetectorRef) {
    super();
    this.tabsData = homeTabsData();
  }

  ngOnInit() {
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

  refreshNow() { this.fetch().subscribe(); }

  setDays(d: number) {
    if (d === this.days) { return; }
    this.days = d;
    this.fetch().subscribe();
  }

  setViewMode(m: ViewMode) {
    this.viewMode = m;
  }

  setHideEdges(hide: boolean) {
    this.hideEdges = hide;
  }

  toggleVisor(v: VisorNode) { v.expanded = !v.expanded; }

  private fetch() {
    this.loading = this.byTransport.length === 0 && this.byVisor.length === 0;
    return this.api.get(`network/transports?days=${this.days}`).pipe(
      catchError((err) => {
        this.error = err?.message || 'Failed to fetch transports';
        this.loading = false;
        this.cdr.markForCheck();
        return of(null);
      }),
      switchMap((rows) => {
        if (rows === null) { return of(null); }
        this.consume(Array.isArray(rows) ? rows : []);
        return of(rows);
      }),
    );
  }

  private consume(metrics: TransportMetric[]) {
    this.rawCount = metrics.length;
    let networkBw = 0;
    const byTp: ByTransportRow[] = [];
    const byVisorMap = new Map<string, VisorNode>();

    for (const m of metrics) {
      if (!m.edges || m.edges.length < 2) { continue; }
      const [aToB, bToA] = this.verifiedBandwidth(m);
      const bw = aToB + bToA;
      networkBw += bw;
      if (bw === 0 && !m.latency) { continue; }

      byTp.push({
        id: m.id,
        type: m.type,
        edge_a: m.edges[0],
        edge_b: m.edges[1],
        sent: aToB,
        recv: bToA,
        bandwidth: bw,
        latency: m.latency,
      });

      // Edge A perspective.
      const a = byVisorMap.get(m.edges[0]) || this.newVisorNode(m.edges[0]);
      a.sent += aToB; a.recv += bToA; a.bandwidth += bw;
      a.transports.push({
        id: m.id, type: m.type, remote: m.edges[1],
        sent: aToB, recv: bToA, bandwidth: bw, latency: m.latency,
      });
      byVisorMap.set(m.edges[0], a);

      // Edge B perspective.
      const b = byVisorMap.get(m.edges[1]) || this.newVisorNode(m.edges[1]);
      b.sent += bToA; b.recv += aToB; b.bandwidth += bw;
      b.transports.push({
        id: m.id, type: m.type, remote: m.edges[0],
        sent: bToA, recv: aToB, bandwidth: bw, latency: m.latency,
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
    this.lastUpdated = new Date();
    this.cdr.markForCheck();
  }

  private newVisorNode(pk: string): VisorNode {
    return { pk, sent: 0, recv: 0, bandwidth: 0, transports: [], expanded: false };
  }

  /** Mirrors verifiedBandwidth() in cmd/skywire-cli/commands/tp/tp-metrics.go. */
  private verifiedBandwidth(m: TransportMetric): [number, number] {
    let aToB = 0, bToA = 0;
    for (const d of m.daily || []) {
      const aRep = !!d.a && ((d.a.sent || 0) > 0 || (d.a.recv || 0) > 0);
      const bRep = !!d.b && ((d.b.sent || 0) > 0 || (d.b.recv || 0) > 0);
      if (aRep && bRep) {
        aToB += Math.min(d.a.sent || 0, d.b.recv || 0);
        bToA += Math.min(d.a.recv || 0, d.b.sent || 0);
      } else if (aRep) {
        aToB += d.a.sent || 0;
        bToA += d.a.recv || 0;
      } else if (bRep) {
        aToB += d.b.recv || 0;
        bToA += d.b.sent || 0;
      }
    }
    return [aToB, bToA];
  }

  /** Bytes → human readable (KiB/MiB/GiB). */
  fmtBytes(b: number): string {
    if (!b || b < 0) { return '-'; }
    const u = ['B', 'KB', 'MB', 'GB', 'TB'];
    let i = 0, v = b;
    while (v >= 1024 && i < u.length - 1) { v /= 1024; i++; }
    return v < 10 ? v.toFixed(1) + ' ' + u[i] : Math.round(v) + ' ' + u[i];
  }

  /** Latency µs → "X.Xms" or "Yμs". */
  fmtLatency(l?: TransportLatency): string {
    if (!l || !l.avg) { return '-'; }
    const ms = l.avg / 1000;
    if (ms < 1) { return Math.round(l.avg) + 'μs'; }
    if (ms < 1000) { return ms.toFixed(1) + 'ms'; }
    return (ms / 1000).toFixed(2) + 's';
  }

  fmtLatencyFull(l?: TransportLatency): string {
    if (!l || !l.avg) { return '-'; }
    return (l.min / 1000).toFixed(1) + ' / ' + (l.avg / 1000).toFixed(1) + ' / ' + (l.max / 1000).toFixed(1) + ' ms';
  }

  trackTpId(_: number, e: ByTransportRow): string { return e.id; }
  trackVisorPk(_: number, n: VisorNode): string { return n.pk; }
  trackChildId(_: number, c: VisorChildTp): string { return c.id; }
}
