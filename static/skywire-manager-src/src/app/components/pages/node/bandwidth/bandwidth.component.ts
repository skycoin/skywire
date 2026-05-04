import { Component, OnDestroy, OnInit, ChangeDetectorRef } from '@angular/core';
import { Subscription, interval, startWith } from 'rxjs';
import { switchMap, catchError } from 'rxjs/operators';
import { of } from 'rxjs';

import { Node } from '../../../../app.datatypes';
import { NodeComponent } from '../node.component';
import { PageBaseComponent } from 'src/app/utils/page-base';
import { ApiService } from 'src/app/services/api.service';

/**
 * Per-visor Bandwidth tab. Reads from the visor's local bbolt
 * stats store (pkg/visor/stats) — no TPD round-trip required.
 *
 * The visor's stats tracker samples every transport on a 10s tick
 * (configurable) and persists current snapshot + per-day rollups
 * with a 30-day retention window. This tab renders the current
 * snapshot on top + an expandable per-transport daily history.
 */

interface LiveSnapshot {
  sent_bytes?: number;
  recv_bytes?: number;
  latency_min_ms?: number;
  latency_max_ms?: number;
  latency_avg_ms?: number;
  sampled_at?: string;
}
interface DailyRollup {
  date: string;
  sent_bytes?: number;
  recv_bytes?: number;
  latency_min_ms?: number;
  latency_max_ms?: number;
  latency_avg_ms?: number;
  samples?: number;
}
interface TransportRecord {
  id: string;
  edges?: string[];
  type?: string;
  label?: string;
  first_seen?: string;
  last_seen?: string;
  current?: LiveSnapshot;
  daily?: DailyRollup[];
}

interface Resp {
  transports?: TransportRecord[];
  fetched_at?: string;
}

interface Row extends TransportRecord {
  expanded: boolean;
  totalSent: number;
  totalRecv: number;
  totalBw: number;
}

type WindowDays = 1 | 7 | 30;

@Component({
  selector: 'app-bandwidth',
  templateUrl: './bandwidth.component.html',
  styleUrls: ['./bandwidth.component.scss'],
  standalone: false,
})
export class BandwidthComponent extends PageBaseComponent implements OnInit, OnDestroy {
  node: Node;
  rows: Row[] = [];
  loading = true;
  error: string | null = null;
  fetchedAt: Date | null = null;
  windowDays: WindowDays = 7;

  private nodeSub: Subscription;
  private pollSub: Subscription;

  constructor(private api: ApiService, private cdr: ChangeDetectorRef) {
    super();
  }

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
  }

  setWindow(d: WindowDays) {
    if (d === this.windowDays) { return; }
    this.windowDays = d;
    this.recompute();
  }

  refreshNow() {
    if (!this.node) { return; }
    this.fetchOnce().subscribe();
  }

  toggleRow(r: Row) { r.expanded = !r.expanded; }

  private startPolling() {
    // 30s cadence — local store updates on the visor's 10s sampler,
    // so 30s catches changes promptly without flooding the RPC.
    this.pollSub = interval(30000).pipe(
      startWith(0),
      switchMap(() => this.fetchOnce()),
    ).subscribe();
  }

  private fetchOnce() {
    return this.api.get(`visors/${this.node.localPk}/local-transport-stats`).pipe(
      catchError((err) => {
        this.error = err?.message || 'Failed to fetch bandwidth';
        this.loading = false;
        this.cdr.markForCheck();
        return of(null);
      }),
      switchMap((resp: Resp | null) => {
        if (resp) {
          this.consume(resp);
        }
        return of(resp);
      }),
    );
  }

  private consume(resp: Resp) {
    const tps = (resp.transports || []) as TransportRecord[];
    this.rows = tps.map((t) => this.toRow(t));
    this.recompute();
    this.fetchedAt = resp.fetched_at ? new Date(resp.fetched_at) : new Date();
    this.loading = false;
    this.error = null;
    this.cdr.markForCheck();
  }

  private toRow(t: TransportRecord): Row {
    return { ...t, expanded: false, totalSent: 0, totalRecv: 0, totalBw: 0 };
  }

  /** Recompute totals based on the current windowDays selection.
   *  Day 1 = current snapshot only; Day 7/30 = sum of last N days. */
  private recompute() {
    const cutoff = new Date();
    cutoff.setUTCDate(cutoff.getUTCDate() - this.windowDays);
    const cutoffStr = cutoff.toISOString().slice(0, 10);

    for (const r of this.rows) {
      let sent = 0, recv = 0;
      if (this.windowDays === 1) {
        sent = r.current?.sent_bytes || 0;
        recv = r.current?.recv_bytes || 0;
      } else {
        for (const d of r.daily || []) {
          if (d.date >= cutoffStr) {
            sent += d.sent_bytes || 0;
            recv += d.recv_bytes || 0;
          }
        }
      }
      r.totalSent = sent;
      r.totalRecv = recv;
      r.totalBw = sent + recv;
    }
    // Re-sort rows by total bandwidth descending so the busiest
    // float to the top whenever the window changes.
    this.rows.sort((a, b) => b.totalBw - a.totalBw);
  }

  /** Aggregate bandwidth across all transports for the current window. */
  get totalNetworkBw(): number {
    return this.rows.reduce((sum, r) => sum + r.totalBw, 0);
  }

  /** Format bytes → human readable. */
  fmtBytes(b?: number): string {
    if (b === undefined || b === null) { return '-'; }
    if (b === 0) { return '0 B'; }
    const u = ['B', 'KB', 'MB', 'GB', 'TB'];
    let i = 0; let v = b;
    while (v >= 1024 && i < u.length - 1) { v /= 1024; i++; }
    return v < 10 ? v.toFixed(1) + ' ' + u[i] : Math.round(v) + ' ' + u[i];
  }

  /** Latency ms → "X.Xms" or "Xms". */
  fmtLatency(ms?: number): string {
    if (!ms) { return '-'; }
    if (ms < 10) { return ms.toFixed(2) + ' ms'; }
    return Math.round(ms) + ' ms';
  }

  fmtLatencyTriple(d?: { latency_min_ms?: number, latency_max_ms?: number, latency_avg_ms?: number }): string {
    if (!d || !d.latency_avg_ms) { return '-'; }
    const min = d.latency_min_ms ?? 0;
    const max = d.latency_max_ms ?? 0;
    const avg = d.latency_avg_ms;
    return `${this.fmtLatency(min)} / ${this.fmtLatency(avg)} / ${this.fmtLatency(max)}`;
  }

  /** Remote PK from the edges array — picks the one that isn't us. */
  remotePK(r: Row): string {
    if (!r.edges || !this.node) { return ''; }
    return r.edges.find((e) => e !== this.node.localPk) || r.edges[0] || '';
  }

  trackRow(_: number, r: Row): string { return r.id; }
  trackDay(_: number, d: DailyRollup): string { return d.date; }
}
