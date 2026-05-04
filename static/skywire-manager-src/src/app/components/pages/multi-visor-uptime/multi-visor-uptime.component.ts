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
 * Network-wide Uptime tab on the home page. Iterates the
 * hypervisor's known visors, hits each one's LocalUptimeStats RPC
 * (mirror of /stats/uptime on the visor's logserver), and shows a
 * compact per-visor row with today's bitmap plus tier averages over
 * the selected window.
 *
 * Rendering note: each visor's data comes from its own bbolt store,
 * not from the standalone uptime-tracker service or TPD aggregates —
 * that's the integrated tracking the operator wanted to see (exact
 * five-minute online/offline intervals per visor, locally-recorded).
 *
 * Filter toggle: "connected only" (default — visors with an active
 * hypervisor session) vs "all" (also show offline visors as
 * unreachable rows). The hypervisor doesn't have a remote bbolt
 * store mirror, so offline visors show "no data" until they
 * reconnect. Anything richer would require pulling from TPD/dmsgd
 * snapshots, which is out of scope here.
 */

interface UptimeResp {
  tiers?: { [tier: string]: { [date: string]: string } };
  fetched_at?: string;
}

interface DayCell {
  online: boolean;
  slot: number;
}

interface TierSummary {
  // Average online% across all days in the window.
  pct: number;
  // Today's 288-cell bar (or empty if no data for today).
  todayCells: DayCell[];
}

interface VisorRow {
  node: Node;
  // Tier name → summary. Order is preserved by visiting in fixed order.
  tiers: { [name: string]: TierSummary };
  // Best-effort overall pct: process tier preferred (it's the gating one),
  // else mean of available tiers.
  overallPct: number;
  error?: string;
  fetchedAt?: number;
}

type WindowDays = 1 | 7 | 30;
type FleetFilter = 'connected' | 'all';

const TIER_ORDER = ['process', 'dmsg', 'skynet'];

@Component({
  selector: 'app-multi-visor-uptime',
  templateUrl: './multi-visor-uptime.component.html',
  styleUrls: ['./multi-visor-uptime.component.scss'],
  standalone: false,
})
export class MultiVisorUptimeComponent extends PageBaseComponent implements OnInit, OnDestroy {
  tabsData: TabButtonData[] = [];
  rows: VisorRow[] = [];
  loading = true;
  error: string | null = null;
  lastUpdated: Date | null = null;
  windowDays: WindowDays = 7;
  filter: FleetFilter = 'connected';

  // Cached "all rows" before filter — so toggling filter is instant.
  private allRows: VisorRow[] = [];
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
    // 60s cadence — fan-out per connected visor; tier bitmaps update
    // on the per-visor 5-minute sampler so faster polling is wasted.
    this.sub = interval(60000).pipe(
      startWith(0),
      switchMap(() => this.nodeService.getNodes()),
      switchMap((nodes: Node[]) => this.fetchAll(nodes || [])),
    ).subscribe();
    return super.ngOnInit();
  }

  ngOnDestroy(): void {
    this.sub?.unsubscribe();
  }

  setWindow(d: WindowDays) {
    if (d === this.windowDays) { return; }
    this.windowDays = d;
    // Force a fresh fetch — server-side window matters for the avg%.
    this.refreshNow();
  }

  setFilter(f: FleetFilter) {
    if (f === this.filter) { return; }
    this.filter = f;
    this.applyFilter();
  }

  refreshNow() {
    this.nodeService.getNodes().pipe(
      switchMap((nodes: Node[]) => this.fetchAll(nodes || [])),
    ).subscribe();
  }

  private fetchAll(nodes: Node[]) {
    if (nodes.length === 0) {
      this.allRows = [];
      this.applyFilter();
      this.loading = false;
      this.lastUpdated = new Date();
      this.cdr.markForCheck();
      return of(null);
    }

    const online = nodes.filter((n) => n.online);
    if (online.length === 0) {
      this.allRows = nodes.map((n) => this.emptyRow(n, 'offline'));
      this.applyFilter();
      this.loading = false;
      this.lastUpdated = new Date();
      this.cdr.markForCheck();
      return of(null);
    }

    const until = new Date();
    const since = new Date(until.getTime() - this.windowDays * 86400 * 1000);
    const qs = `?since=${since.toISOString()}&until=${until.toISOString()}`;

    const fetches = online.map((n) =>
      this.api.get(`visors/${n.localPk}/local-uptime-stats${qs}`).pipe(
        catchError((err) => of({ __error: err?.message || 'failed' })),
      ),
    );

    return forkJoin(fetches).pipe(
      switchMap((results: any[]) => {
        const byPk: { [pk: string]: VisorRow } = {};
        for (let i = 0; i < online.length; i++) {
          const node = online[i];
          const result = results[i];
          if (result && result.__error) {
            byPk[node.localPk] = this.emptyRow(node, result.__error);
          } else {
            byPk[node.localPk] = this.buildRow(node, result as UptimeResp);
          }
        }
        // Include offline nodes as empty rows so the "all" filter
        // can show them as unreachable.
        const all: VisorRow[] = nodes.map((n) =>
          byPk[n.localPk] || this.emptyRow(n, 'offline'),
        );
        all.sort((a, b) => b.overallPct - a.overallPct);
        this.allRows = all;
        this.applyFilter();
        this.loading = false;
        this.lastUpdated = new Date();
        this.cdr.markForCheck();
        return of(results);
      }),
    );
  }

  private buildRow(node: Node, resp: UptimeResp): VisorRow {
    const tiers: { [name: string]: TierSummary } = {};
    const todayKey = new Date().toISOString().slice(0, 10);

    let tierCount = 0;
    let pctSum = 0;
    let processPct: number | null = null;
    for (const name of TIER_ORDER) {
      const days = resp.tiers?.[name];
      if (!days) { continue; }
      let onlineSlots = 0;
      let totalSlots = 0;
      const todayAscii = days[todayKey] || '';
      for (const date of Object.keys(days)) {
        const ascii = days[date] || '';
        for (let i = 0; i < ascii.length; i++) {
          if (ascii.charAt(i) === '.') { onlineSlots++; }
          totalSlots++;
        }
      }
      const pct = totalSlots > 0 ? (onlineSlots / totalSlots) * 100 : 0;
      tiers[name] = {
        pct,
        todayCells: this.cellsFromAscii(todayAscii),
      };
      if (name === 'process') { processPct = pct; }
      pctSum += pct;
      tierCount++;
    }
    const overallPct = processPct !== null ? processPct : (tierCount > 0 ? pctSum / tierCount : 0);
    return {
      node,
      tiers,
      overallPct,
      fetchedAt: resp.fetched_at ? new Date(resp.fetched_at).getTime() : Date.now(),
    };
  }

  private emptyRow(node: Node, error: string): VisorRow {
    return { node, tiers: {}, overallPct: 0, error };
  }

  private cellsFromAscii(ascii: string): DayCell[] {
    const expected = 288;
    let s = ascii || '';
    if (s.length < expected) { s = s.padEnd(expected, ' '); }
    if (s.length > expected) { s = s.slice(0, expected); }
    const cells: DayCell[] = new Array(expected);
    for (let i = 0; i < expected; i++) {
      cells[i] = { online: s.charAt(i) === '.', slot: i };
    }
    return cells;
  }

  private applyFilter() {
    if (this.filter === 'connected') {
      this.rows = this.allRows.filter((r) => r.node.online);
    } else {
      this.rows = this.allRows.slice();
    }
    this.cdr.markForCheck();
  }

  fmtPct(p: number): string {
    if (p >= 99.95) { return '100%'; }
    if (p > 0 && p < 1) { return '<1%'; }
    return p.toFixed(1) + '%';
  }

  pctClass(p: number): string {
    if (p >= 99) { return 'up-good'; }
    if (p >= 80) { return 'up-mid'; }
    return 'up-bad';
  }

  cellTooltip(c: DayCell): string {
    const start = this.fmtSlot(c.slot);
    const end = this.fmtSlot(Math.min(c.slot + 1, 288));
    return `${start}–${end} UTC: ${c.online ? 'online' : 'offline'}`;
  }

  private fmtSlot(slot: number): string {
    const minutes = slot * 5;
    const hh = Math.floor(minutes / 60).toString().padStart(2, '0');
    const mm = (minutes % 60).toString().padStart(2, '0');
    return `${hh}:${mm}`;
  }

  tierNames(): string[] { return TIER_ORDER; }
  trackRow(_: number, r: VisorRow): string { return r.node.localPk; }
  trackCell(_: number, c: DayCell): number { return c.slot; }
}
