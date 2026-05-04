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
 * the visor's logserver.
 *
 * Layout: one row per UTC day (newest first), with the three tiers
 * — process / dmsg / skynet — rendered as adjacent ribbons under a
 * shared date header. Each ribbon has 288 cells at 5-minute slot
 * resolution; "future" slots on today's row are explicitly distinct
 * from "offline" so an empty bar on today doesn't read as downtime.
 *
 * Tier semantics:
 *   process — visor process is up (the local sampler ticked).
 *   dmsg    — dmsg client connected to a server.
 *   skynet  — visor has ≥ 2 live transports (matches TPD's
 *             "skynet online" criterion: a visor is skynet-routable
 *             only when it has at least two transports for traffic
 *             to actually flow through).
 */

interface UptimeResp {
  tiers?: { [tier: string]: { [date: string]: string } };
  since?: string;
  until?: string;
  fetched_at?: string;
}

interface DayCell {
  state: 'on' | 'off' | 'future';
  slot: number;
}

interface TierLine {
  name: string;
  cells: DayCell[];
  // Percentage online out of *known* slots only (excludes future slots).
  pct: number;
  // True when this tier had no data recorded for this day at all.
  empty: boolean;
}

interface DayBlock {
  date: string;
  // True when this is today's UTC date — used to mark trailing
  // slots as "future" instead of "offline".
  isToday: boolean;
  tiers: TierLine[];
}

const TIER_ORDER = ['process', 'dmsg', 'skynet'];

type WindowDays = 1 | 7 | 30;

@Component({
  selector: 'app-uptime',
  templateUrl: './uptime.component.html',
  styleUrls: ['./uptime.component.scss'],
  standalone: false,
})
export class UptimeComponent extends PageBaseComponent implements OnInit, OnDestroy {
  node: Node;
  days: DayBlock[] = [];
  loading = true;
  error: string | null = null;
  fetchedAt: Date | null = null;
  windowDays: WindowDays = 7;

  // Per-tier window averages (across visible past + completed slots
  // of today). Kept in render order so the summary strip stays
  // tier-aligned with the day blocks below.
  summary: { name: string; pct: number }[] = [];

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
    const todayKey = new Date().toISOString().slice(0, 10);
    // Slot index inside today's day where "now" falls. Cells at or
    // beyond this are future, regardless of the tier's bitmap.
    const now = new Date();
    const nowSlot = Math.floor((now.getUTCHours() * 60 + now.getUTCMinutes()) / 5);

    // Collect every date that any tier reported.
    const dateSet = new Set<string>();
    for (const name of TIER_ORDER) {
      const days = tiers[name];
      if (!days) { continue; }
      for (const d of Object.keys(days)) { dateSet.add(d); }
    }
    const sortedDates = Array.from(dateSet).sort().reverse(); // newest first

    const out: DayBlock[] = [];
    // Per-tier accumulators for the summary strip.
    const onlineByTier: { [name: string]: number } = {};
    const knownByTier: { [name: string]: number } = {};

    for (const date of sortedDates) {
      const isToday = date === todayKey;
      const tierLines: TierLine[] = [];
      for (const name of TIER_ORDER) {
        const ascii = (tiers[name] && tiers[name][date]) || '';
        const cells = this.buildCells(ascii, isToday, nowSlot);
        let online = 0;
        let known = 0;
        for (const c of cells) {
          if (c.state === 'future') { continue; }
          known++;
          if (c.state === 'on') { online++; }
        }
        tierLines.push({
          name,
          cells,
          pct: known > 0 ? (online / known) * 100 : 0,
          empty: !ascii,
        });
        onlineByTier[name] = (onlineByTier[name] || 0) + online;
        knownByTier[name] = (knownByTier[name] || 0) + known;
      }
      out.push({ date, isToday, tiers: tierLines });
    }

    this.days = out;
    this.summary = TIER_ORDER.map((name) => ({
      name,
      pct: knownByTier[name] > 0 ? (onlineByTier[name] / knownByTier[name]) * 100 : 0,
    }));
    this.fetchedAt = resp.fetched_at ? new Date(resp.fetched_at) : new Date();
    this.loading = false;
    this.error = null;
    this.cdr.markForCheck();
  }

  private buildCells(ascii: string, isToday: boolean, nowSlot: number): DayCell[] {
    const expected = 288;
    let s = ascii || '';
    if (s.length < expected) { s = s.padEnd(expected, ' '); }
    if (s.length > expected) { s = s.slice(0, expected); }
    const cells: DayCell[] = new Array(expected);
    for (let i = 0; i < expected; i++) {
      let state: DayCell['state'];
      if (isToday && i >= nowSlot) {
        // Don't conflate future slots with offline — the bitmap
        // will read ' ' for them simply because they haven't
        // happened yet, which says nothing about reachability.
        state = 'future';
      } else {
        state = s.charAt(i) === '.' ? 'on' : 'off';
      }
      cells[i] = { state, slot: i };
    }
    return cells;
  }

  fmtSlot(slot: number): string {
    const minutes = slot * 5;
    const hh = Math.floor(minutes / 60).toString().padStart(2, '0');
    const mm = (minutes % 60).toString().padStart(2, '0');
    return `${hh}:${mm}`;
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

  fmtPct(p: number): string {
    if (p >= 99.95) { return '100%'; }
    if (p > 0 && p < 1) { return '<1%'; }
    return p.toFixed(1) + '%';
  }

  pctClass(p: number, empty: boolean = false): string {
    if (empty) { return 'dim'; }
    if (p >= 99) { return 'up-good'; }
    if (p >= 80) { return 'up-mid'; }
    return 'up-bad';
  }

  tierLabel(name: string): string { return 'uptime.tier-' + name; }
  tierInfo(name: string): string { return 'uptime.tier-' + name + '-info'; }

  trackDay(_: number, d: DayBlock): string { return d.date; }
  trackTier(_: number, t: TierLine): string { return t.name; }
  trackCell(_: number, c: DayCell): number { return c.slot; }
}
