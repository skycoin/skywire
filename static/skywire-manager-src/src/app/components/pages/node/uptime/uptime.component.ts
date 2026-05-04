import { Component, OnDestroy, OnInit, ChangeDetectorRef } from '@angular/core';
import { Subscription, interval, startWith, of } from 'rxjs';
import { switchMap, catchError } from 'rxjs/operators';

import { Node } from '../../../../app.datatypes';
import { NodeComponent } from '../node.component';
import { PageBaseComponent } from 'src/app/utils/page-base';
import { ApiService } from 'src/app/services/api.service';

/**
 * Per-visor Uptime tab. Reads from the visor's local bbolt stats
 * store via the LocalUptimeStats RPC, mirroring `/stats/uptime` on
 * the visor's logserver. The store keeps a 288-bit-per-day bitmap
 * per tier (process / dmsg / skynet) at 5-minute slot resolution;
 * we render each day as a 288-cell ribbon plus an uptime percentage.
 *
 * Tier semantics:
 *   process — visor process is up (the local sampler ticked).
 *   dmsg    — dmsg client connected to a server.
 *   skynet  — skynet connectivity probe succeeded.
 *
 * The "exact intervals where the visor was online and offline" the
 * operator asked for is precisely a row of these cells: each cell
 * spans a five-minute UTC window, set when the tier was observed
 * online during at least one sampler tick within that window.
 */

interface UptimeResp {
  tiers?: { [tier: string]: { [date: string]: string } };
  since?: string;
  until?: string;
  fetched_at?: string;
}

interface DayCell {
  online: boolean;
  // 5-minute slot index (0..287); used for tooltip text only.
  slot: number;
}

interface DayRow {
  date: string;
  cells: DayCell[];
  onlineSlots: number;
  totalSlots: number;
  pct: number;
}

interface TierBlock {
  name: string;
  days: DayRow[];
  // Aggregate online% across all visible days in this tier.
  avgPct: number;
}

type WindowDays = 1 | 7 | 30;

@Component({
  selector: 'app-uptime',
  templateUrl: './uptime.component.html',
  styleUrls: ['./uptime.component.scss'],
  standalone: false,
})
export class UptimeComponent extends PageBaseComponent implements OnInit, OnDestroy {
  node: Node;
  tiers: TierBlock[] = [];
  loading = true;
  error: string | null = null;
  fetchedAt: Date | null = null;
  windowDays: WindowDays = 7;

  private nodeSub: Subscription;
  private pollSub: Subscription;

  constructor(private api: ApiService, private cdr: ChangeDetectorRef) { super(); }

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
    this.refreshNow();
  }

  refreshNow() {
    if (!this.node) { return; }
    this.fetchOnce().subscribe();
  }

  private startPolling() {
    // 60s cadence: tier bitmaps tick on the visor's 5-minute sampler,
    // so polling faster wastes RPC; slower delays "now" by too much.
    this.pollSub = interval(60000).pipe(
      startWith(0),
      switchMap(() => this.fetchOnce()),
    ).subscribe();
  }

  private fetchOnce() {
    const until = new Date();
    const since = new Date(until.getTime() - this.windowDays * 86400 * 1000);
    const qs = `?since=${since.toISOString()}&until=${until.toISOString()}`;
    return this.api.get(`visors/${this.node.localPk}/local-uptime-stats${qs}`).pipe(
      catchError((err) => {
        this.error = err?.message || 'Failed to fetch uptime';
        this.loading = false;
        this.cdr.markForCheck();
        return of(null);
      }),
      switchMap((resp: UptimeResp | null) => {
        if (resp) { this.consume(resp); }
        return of(resp);
      }),
    );
  }

  private consume(resp: UptimeResp) {
    const tiers = resp.tiers || {};
    const out: TierBlock[] = [];
    for (const name of Object.keys(tiers).sort(this.tierSort)) {
      const days: DayRow[] = [];
      for (const date of Object.keys(tiers[name]).sort()) {
        days.push(this.buildDayRow(date, tiers[name][date]));
      }
      const totalOnline = days.reduce((s, d) => s + d.onlineSlots, 0);
      const totalSlots = days.reduce((s, d) => s + d.totalSlots, 0);
      out.push({
        name,
        days,
        avgPct: totalSlots > 0 ? (totalOnline / totalSlots) * 100 : 0,
      });
    }
    this.tiers = out;
    this.fetchedAt = resp.fetched_at ? new Date(resp.fetched_at) : new Date();
    this.loading = false;
    this.error = null;
    this.cdr.markForCheck();
  }

  /** Order tiers in a sensible visual stack — process first since
   *  it gates the rest, then network layers in dependency order. */
  private tierSort(a: string, b: string): number {
    const order = ['process', 'dmsg', 'skynet'];
    const ia = order.indexOf(a);
    const ib = order.indexOf(b);
    if (ia >= 0 && ib >= 0) { return ia - ib; }
    if (ia >= 0) { return -1; }
    if (ib >= 0) { return 1; }
    return a.localeCompare(b);
  }

  private buildDayRow(date: string, ascii: string): DayRow {
    // The wire form is exactly 288 chars; defensively pad/trim if
    // the server ever changes the shape so the row still renders.
    const expected = 288;
    let s = ascii || '';
    if (s.length < expected) { s = s.padEnd(expected, ' '); }
    if (s.length > expected) { s = s.slice(0, expected); }

    const cells: DayCell[] = new Array(expected);
    let online = 0;
    for (let i = 0; i < expected; i++) {
      const on = s.charAt(i) === '.';
      if (on) { online++; }
      cells[i] = { online: on, slot: i };
    }
    return {
      date,
      cells,
      onlineSlots: online,
      totalSlots: expected,
      pct: (online / expected) * 100,
    };
  }

  /** Format a slot index (0..287) → "HH:MM" UTC string for tooltip. */
  fmtSlot(slot: number): string {
    const minutes = slot * 5;
    const hh = Math.floor(minutes / 60).toString().padStart(2, '0');
    const mm = (minutes % 60).toString().padStart(2, '0');
    return `${hh}:${mm}`;
  }

  cellTooltip(c: DayCell): string {
    const start = this.fmtSlot(c.slot);
    const end = this.fmtSlot(Math.min(c.slot + 1, 288));
    return `${start}–${end} UTC: ${c.online ? 'online' : 'offline'}`;
  }

  fmtPct(p: number): string {
    if (p >= 99.95) { return '100%'; }
    if (p < 1 && p > 0) { return '<1%'; }
    return p.toFixed(1) + '%';
  }

  trackTier(_: number, t: TierBlock): string { return t.name; }
  trackDay(_: number, d: DayRow): string { return d.date; }
  trackCell(_: number, c: DayCell): number { return c.slot; }
}
