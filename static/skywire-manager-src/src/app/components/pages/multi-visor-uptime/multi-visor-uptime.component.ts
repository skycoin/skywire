import { Component, OnDestroy, OnInit, ChangeDetectorRef } from '@angular/core';
import { Subscription, interval, startWith, of, forkJoin } from 'rxjs';
import { switchMap, catchError } from 'rxjs/operators';

import { TabButtonData } from '../../layout/top-bar/top-bar.component';
import { PageBaseComponent } from 'src/app/utils/page-base';
import { homeTabsData } from 'src/app/utils/home-tabs';
import { NodeService } from 'src/app/services/node.service';
import { ApiService } from 'src/app/services/api.service';
import { Node } from 'src/app/app.datatypes';

/**
 * Network-wide Uptime tab. Source of truth is the TPD integrated
 * uptime tracker (transports + dmsg-discovery heartbeats), surfaced
 * to the hypervisor through `/api/network/visor-uptime` — which
 * itself reads from a lazy on-demand CXO subscriber connected to
 * TPD's uptime publisher (skyenv.DmsgTPDUptimeCXOPort), falling
 * back to DMSG-HTTP and plain HTTP when the cache is cold.
 *
 * Response shape (v3): `[{pk, on, version, daily: {date: pct},
 * timeline: {date: 288-char-bitmap}}, ...]`. The 288-char timeline
 * is the "exact intervals where the visor was online and offline"
 * the operator asked for, recorded by the integrated tracker
 * rather than the standalone uptime-tracker service.
 *
 * Filter toggle:
 *   "connected only" — restrict to PKs the hypervisor currently has
 *     an RPC session with. Useful when the operator only cares
 *     about their own fleet.
 *   "all known" — every PK TPD reports for the day window, regardless
 *     of whether this hypervisor manages it.
 */

interface VisorSummary {
  pk: string;
  on: boolean;
  version?: string;
  daily?: { [date: string]: string };
  timeline?: { [date: string]: string };
}

interface DayCell {
  state: 'on' | 'off' | 'future';
  slot: number;
}

interface VisorRow {
  pk: string;
  online: boolean;
  version: string;
  // Today's 288-cell ribbon for the at-a-glance bar.
  todayCells: DayCell[];
  // Window-aggregate: percentage of *known* (non-future) slots that
  // were online across all days in the response.
  windowPct: number;
  // Most-recent-day percentage (today, if available; else newest).
  recentPct: number;
  // True when this row is also a hypervisor-managed visor.
  managed: boolean;
  // Hypervisor-side label, when known.
  label?: string;
}

type WindowDays = 1 | 7 | 30;
type FleetFilter = 'connected' | 'all';

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
  filter: FleetFilter = 'all';
  source: string = '';

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
    // 60s cadence — matches the TPD publisher's recompute tick.
    this.sub = interval(60000).pipe(
      startWith(0),
      switchMap(() => this.fetchOnce()),
    ).subscribe();
    return super.ngOnInit();
  }

  ngOnDestroy(): void {
    this.sub?.unsubscribe();
  }

  setWindow(d: WindowDays) {
    if (d === this.windowDays) { return; }
    this.windowDays = d;
    this.refreshNow();
  }

  setFilter(f: FleetFilter) {
    if (f === this.filter) { return; }
    this.filter = f;
    this.applyFilter();
  }

  refreshNow() {
    this.fetchOnce().subscribe();
  }

  private fetchOnce() {
    // Fetch the network uptime feed and the local nodes list in
    // parallel — the nodes list lets us mark "managed" rows and
    // surfaces hypervisor-side labels for the connected-only filter.
    return forkJoin({
      summaries: this.api.get(`network/visor-uptime?days=${this.windowDays}`).pipe(
        catchError((err) => {
          this.error = err?.message || 'Failed to fetch network uptime';
          this.loading = false;
          this.cdr.markForCheck();
          return of([]);
        }),
      ),
      nodes: this.nodeService.getNodes().pipe(catchError(() => of([] as Node[]))),
    }).pipe(
      switchMap(({ summaries, nodes }) => {
        this.consume((summaries as VisorSummary[]) || [], nodes || []);
        return of(null);
      }),
    );
  }

  private consume(summaries: VisorSummary[], nodes: Node[]) {
    const managedByPk: { [pk: string]: Node } = {};
    for (const n of nodes) {
      managedByPk[n.localPk] = n;
    }
    const todayKey = new Date().toISOString().slice(0, 10);
    const now = new Date();
    const nowSlot = Math.floor((now.getUTCHours() * 60 + now.getUTCMinutes()) / 5);

    const out: VisorRow[] = [];
    for (const s of summaries) {
      const days = s.timeline || {};
      const dailyPcts = s.daily || {};

      // Aggregate window pct from timeline (counting non-future slots
      // only). Falls back to averaging the daily-percent dictionary
      // when the row has no timeline (TPD v3 still emits timelines
      // for known PKs only — unknowns degrade to v2 shape).
      let online = 0;
      let known = 0;
      for (const date of Object.keys(days)) {
        const ascii = days[date] || '';
        const isToday = date === todayKey;
        for (let i = 0; i < ascii.length; i++) {
          if (isToday && i >= nowSlot) { continue; }
          known++;
          if (ascii.charAt(i) === '.') { online++; }
        }
      }
      let windowPct = known > 0 ? (online / known) * 100 : 0;
      if (known === 0 && Object.keys(dailyPcts).length > 0) {
        let sum = 0;
        let n = 0;
        for (const date of Object.keys(dailyPcts)) {
          const v = parseFloat(dailyPcts[date]);
          if (!isNaN(v)) { sum += v; n++; }
        }
        windowPct = n > 0 ? sum / n : 0;
      }

      // Most-recent-day percentage. Prefer today if it has any data,
      // else the most recent date with a non-zero timeline.
      let recentPct = 0;
      if (dailyPcts[todayKey] !== undefined) {
        const v = parseFloat(dailyPcts[todayKey]);
        recentPct = isNaN(v) ? 0 : v;
      } else if (days[todayKey]) {
        recentPct = this.pctFromAscii(days[todayKey], true, nowSlot);
      } else {
        const sortedDates = Object.keys(days).sort().reverse();
        for (const d of sortedDates) {
          if (days[d]) {
            recentPct = this.pctFromAscii(days[d], false, 288);
            break;
          }
        }
      }

      const todayCells = this.cellsFromAscii(days[todayKey] || '', true, nowSlot);
      const managed = !!managedByPk[s.pk];
      out.push({
        pk: s.pk,
        online: s.on,
        version: s.version || '',
        todayCells,
        windowPct,
        recentPct,
        managed,
        label: managed ? (managedByPk[s.pk].label || '') : '',
      });
    }

    out.sort((a, b) => b.windowPct - a.windowPct);
    this.allRows = out;
    this.applyFilter();
    this.loading = false;
    this.error = null;
    this.lastUpdated = new Date();
    this.cdr.markForCheck();
  }

  private pctFromAscii(ascii: string, isToday: boolean, nowSlot: number): number {
    let online = 0;
    let known = 0;
    for (let i = 0; i < ascii.length; i++) {
      if (isToday && i >= nowSlot) { continue; }
      known++;
      if (ascii.charAt(i) === '.') { online++; }
    }
    return known > 0 ? (online / known) * 100 : 0;
  }

  private cellsFromAscii(ascii: string, isToday: boolean, nowSlot: number): DayCell[] {
    const expected = 288;
    let s = ascii || '';
    if (s.length < expected) { s = s.padEnd(expected, ' '); }
    if (s.length > expected) { s = s.slice(0, expected); }
    const cells: DayCell[] = new Array(expected);
    for (let i = 0; i < expected; i++) {
      let state: DayCell['state'];
      if (isToday && i >= nowSlot) {
        state = 'future';
      } else {
        state = s.charAt(i) === '.' ? 'on' : 'off';
      }
      cells[i] = { state, slot: i };
    }
    return cells;
  }

  private applyFilter() {
    if (this.filter === 'connected') {
      this.rows = this.allRows.filter((r) => r.managed);
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
    let label: string;
    switch (c.state) {
      case 'on': label = 'online'; break;
      case 'off': label = 'offline'; break;
      default: label = 'future';
    }
    return `${start}–${end} UTC: ${label}`;
  }

  private fmtSlot(slot: number): string {
    const minutes = slot * 5;
    const hh = Math.floor(minutes / 60).toString().padStart(2, '0');
    const mm = (minutes % 60).toString().padStart(2, '0');
    return `${hh}:${mm}`;
  }

  trackRow(_: number, r: VisorRow): string { return r.pk; }
  trackCell(_: number, c: DayCell): number { return c.slot; }
}
