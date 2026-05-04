import { Component, OnDestroy, OnInit, ChangeDetectorRef } from '@angular/core';
import { Subscription, interval, startWith, of, forkJoin, timer } from 'rxjs';
import { switchMap, catchError, takeUntil } from 'rxjs/operators';

import { TabButtonData } from '../../layout/top-bar/top-bar.component';
import { PageBaseComponent } from 'src/app/utils/page-base';
import { homeTabsData } from 'src/app/utils/home-tabs';
import { NodeService } from 'src/app/services/node.service';
import { ApiService } from 'src/app/services/api.service';
import { Node } from 'src/app/app.datatypes';

/**
 * Network-wide Uptime tab. Source of truth: TPD's integrated uptime
 * tracker, surfaced through `/api/network/visor-uptime` (CXO
 * subscriber → DMSG-HTTP → HTTP fallback chain on the visor side).
 *
 * Render shape matches `skywire cli ut tpd graph` (default mode):
 * one line per visor, "<pk> <concatenated bar>". Each bar is the
 * visor's full available timeline drawn as 24 hour blocks per day,
 * shaded by online-slot count within the hour:
 *
 *     0 slots → blank        (gap)
 *     1–3     → faint        (mostly down)
 *     4–6     → mid          (intermittent)
 *     7–9     → dense        (mostly up)
 *     10–12   → full         (solid up)
 *
 * Today's trailing hours that haven't happened yet render as the
 * future-cell style — explicitly distinct from "offline" so a
 * partial today doesn't read as downtime.
 *
 * The CLI version trims shared leading/trailing whitespace globally
 * across all bars (the period when no visor had data yet, and the
 * unfilled hours past "now"); we do the same so the bars line up
 * and the window doesn't waste a third of its width on dead space.
 */

interface VisorSummary {
  pk: string;
  on: boolean;
  version?: string;
  daily?: { [date: string]: string };
  timeline?: { [date: string]: string };
}

// One column of a visor's bar — either a real hour block (slot >= 0)
// or a future placeholder (slot < 0) for trailing today-hours.
interface BarBlock {
  // 0..12 — number of online slots within the hour. -1 means future.
  count: number;
  // Tooltip-only date+hour, e.g. "2026-05-03 14:00 UTC".
  label: string;
  future: boolean;
}

interface VisorRow {
  pk: string;
  online: boolean;
  version: string;
  blocks: BarBlock[];
  // Window-aggregate: percentage of *known* (non-future) slots that
  // were online across all days in the response.
  windowPct: number;
  // Most-recent-day percentage — prefer TPD's daily field, fall back
  // to a derive-from-bitmap when the daily map is missing.
  recentPct: number;
  // True when this row is also a hypervisor-managed visor.
  managed: boolean;
  label?: string;
}

type WindowDays = 1 | 7 | 30;
type FleetFilter = 'connected' | 'all';

const HOURS_PER_DAY = 24;
const SLOTS_PER_HOUR = 12;
const SLOTS_PER_DAY = HOURS_PER_DAY * SLOTS_PER_HOUR;
// Hard upper bound for the network/visor-uptime fetch. The handler's
// CXO/DMSG-HTTP/HTTP fallback chain can sit on a hung dial; this just
// surfaces an error after 30s instead of leaving the spinner forever.
const FETCH_TIMEOUT_MS = 30000;

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
  // Hour-tick row drawn above the bars when there's anything to show.
  // Same contents for every row, so we render once at the top.
  ticks: { label: string; left: number }[] = [];
  totalBlocks = 0;

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
    return forkJoin({
      summaries: this.api.get(`network/visor-uptime?days=${this.windowDays}`).pipe(
        // Bound the wait — a stuck TPD shouldn't park the spinner.
        takeUntil(timer(FETCH_TIMEOUT_MS)),
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
    for (const n of nodes) { managedByPk[n.localPk] = n; }

    // Union of all dates the server returned, sorted ascending so
    // bars read left-to-right oldest → newest like the CLI.
    const dateSet = new Set<string>();
    for (const s of summaries) {
      for (const d of Object.keys(s.timeline || {})) { dateSet.add(d); }
      for (const d of Object.keys(s.daily || {})) { dateSet.add(d); }
    }
    const dates = Array.from(dateSet).sort();

    // Slot index inside today's day where "now" falls.
    const todayKey = new Date().toISOString().slice(0, 10);
    const now = new Date();
    const nowSlot = Math.floor((now.getUTCHours() * 60 + now.getUTCMinutes()) / 5);
    const nowHour = Math.floor(nowSlot / SLOTS_PER_HOUR);

    const out: VisorRow[] = [];
    for (const s of summaries) {
      const blocks = this.buildBlocks(s, dates, todayKey, nowSlot, nowHour);

      // Window aggregate from the non-future hours.
      let online = 0;
      let known = 0;
      for (const b of blocks) {
        if (b.future) { continue; }
        // Each block has up to SLOTS_PER_HOUR underlying samples.
        online += b.count;
        known += SLOTS_PER_HOUR;
      }
      const windowPct = known > 0 ? (online / known) * 100 : 0;

      // Most-recent-day percentage. Prefer the TPD-reported daily%
      // field; fall back to deriving from today's bitmap if missing.
      let recentPct = 0;
      const dailyMap = s.daily || {};
      if (dailyMap[todayKey] !== undefined) {
        const v = parseFloat(dailyMap[todayKey]);
        recentPct = isNaN(v) ? 0 : v;
      } else {
        const sorted = Object.keys(dailyMap).sort().reverse();
        for (const d of sorted) {
          const v = parseFloat(dailyMap[d]);
          if (!isNaN(v)) { recentPct = v; break; }
        }
      }

      const managed = !!managedByPk[s.pk];
      out.push({
        pk: s.pk,
        online: s.on,
        version: s.version || '',
        blocks,
        windowPct,
        recentPct,
        managed,
        label: managed ? (managedByPk[s.pk].label || '') : '',
      });
    }

    // Trim shared leading + trailing empty hours globally so every
    // bar lines up. CLI does the same — when no one has data for the
    // earliest hours of the window, those columns are dead width.
    this.applyTrimmed(out);

    out.sort((a, b) => b.windowPct - a.windowPct);
    this.allRows = out;
    this.totalBlocks = out.length > 0 ? out[0].blocks.length : 0;
    this.ticks = this.buildTicks(this.totalBlocks);
    this.applyFilter();
    this.loading = false;
    this.error = null;
    this.lastUpdated = new Date();
    this.cdr.markForCheck();
  }

  private buildBlocks(
    s: VisorSummary,
    dates: string[],
    todayKey: string,
    nowSlot: number,
    nowHour: number,
  ): BarBlock[] {
    const out: BarBlock[] = [];
    for (const date of dates) {
      const ascii = (s.timeline && s.timeline[date]) || '';
      const padded = ascii.length >= SLOTS_PER_DAY
        ? ascii.slice(0, SLOTS_PER_DAY)
        : ascii.padEnd(SLOTS_PER_DAY, ' ');
      const isToday = date === todayKey;
      for (let h = 0; h < HOURS_PER_DAY; h++) {
        const blockStart = h * SLOTS_PER_HOUR;
        const blockEnd = blockStart + SLOTS_PER_HOUR;
        let count = 0;
        for (let i = blockStart; i < blockEnd; i++) {
          if (padded.charAt(i) === '.') { count++; }
        }
        let future = false;
        if (isToday && h > nowHour) {
          // Hour entirely in the future.
          future = true;
          count = -1;
        } else if (isToday && h === nowHour && nowSlot < blockEnd) {
          // Mixed: count only the slots up to nowSlot.
          count = 0;
          for (let i = blockStart; i < Math.min(blockEnd, nowSlot); i++) {
            if (padded.charAt(i) === '.') { count++; }
          }
        }
        const label = `${date} ${this.fmtHour(h)} UTC`;
        out.push({ count, future, label });
      }
    }
    return out;
  }

  private fmtHour(h: number): string {
    return h.toString().padStart(2, '0') + ':00';
  }

  // Trim columns that are empty (count=0 AND not future) for every
  // row in the dataset, on both ends. Mirrors the CLI's
  // printSingleLineTimelines behaviour.
  private applyTrimmed(rows: VisorRow[]) {
    if (rows.length === 0) { return; }
    const len = rows[0].blocks.length;
    if (len === 0) { return; }

    const sharedEmpty = (idx: number): boolean => {
      for (const r of rows) {
        const b = r.blocks[idx];
        if (!b) { return false; }
        if (b.future) { return false; }
        if (b.count > 0) { return false; }
      }
      return true;
    };

    let lead = 0;
    while (lead < len && sharedEmpty(lead)) { lead++; }
    let trail = 0;
    while (trail < len - lead && sharedEmpty(len - 1 - trail)) { trail++; }

    if (lead === 0 && trail === 0) { return; }
    for (const r of rows) {
      r.blocks = r.blocks.slice(lead, len - trail);
    }
  }

  // Build tick positions — every 24 blocks (one per day boundary)
  // until the dataset ends, plus a final "now" marker.
  private buildTicks(totalBlocks: number): { label: string; left: number }[] {
    if (totalBlocks <= 0) { return []; }
    const out: { label: string; left: number }[] = [];
    // Every-24 is "1d" boundary; only label when total >= 48 to avoid clutter.
    if (totalBlocks >= 48) {
      let day = 1;
      for (let i = 24; i < totalBlocks; i += 24) {
        out.push({ label: `+${day}d`, left: (i / totalBlocks) * 100 });
        day++;
      }
    }
    return out;
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

  // Map an online-slots-per-hour count (0..12) to one of five density
  // classes. Same threshold layout the CLI uses for unicode block art.
  blockClass(b: BarBlock): string {
    if (b.future) { return 'future'; }
    if (b.count <= 0) { return 'lvl0'; }
    if (b.count <= 3) { return 'lvl1'; }
    if (b.count <= 6) { return 'lvl2'; }
    if (b.count <= 9) { return 'lvl3'; }
    return 'lvl4';
  }

  blockTooltip(b: BarBlock): string {
    if (b.future) { return `${b.label} — future`; }
    if (b.count < 0) { return `${b.label} — no data`; }
    return `${b.label} — ${b.count}/12 slots online`;
  }

  trackRow(_: number, r: VisorRow): string { return r.pk; }
  trackBlock(i: number, _b: BarBlock): number { return i; }
}
