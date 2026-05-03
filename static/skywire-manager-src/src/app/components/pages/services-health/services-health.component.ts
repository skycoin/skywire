import { Component, OnDestroy, OnInit } from '@angular/core';
import { Subscription, interval, startWith } from 'rxjs';
import { switchMap } from 'rxjs/operators';

import { ServiceHealthEntry, ServiceHealthService } from 'src/app/services/service-health.service';
import { TabButtonData } from '../../layout/top-bar/top-bar.component';
import { PageBaseComponent } from 'src/app/utils/page-base';
import { ApiService } from 'src/app/services/api.service';
import { homeTabsData } from 'src/app/utils/home-tabs';

/** One RSN's remote /stats fetch result, mirrors pkg/visor.RSNRemoteStat. */
interface RSNRemoteStat {
  pk: string;
  snapshot?: RSNSnapshot;
  error?: string;
  status?: number;
}
/** Subset of pkg/router/setupmetrics.StatsSnapshot we render. */
interface RSNSnapshot {
  started_at?: string;
  uptime_sec?: number;
  total_requests?: number;
  successful?: number;
  failed?: number;
  concurrency_drops?: number;
  active_requests?: number;
  success_rate_pct?: number;
  failures_by_reason?: { [reason: string]: number };
  latency_ms?: {
    count?: number; min_ms?: number; max_ms?: number; mean_ms?: number;
    p50_ms?: number; p95_ms?: number; p99_ms?: number;
  };
  last_success_at?: string;
  last_failure_at?: string;
}

/**
 * Services health dashboard. Shows the reachability, latency and version
 * of every deployment service the local visor is configured to use:
 * Transport Discovery, DMSG Discovery, Address Resolver, Route Finder,
 * Uptime Tracker, Service Discovery. Polled every 15s.
 */
@Component({
  selector: 'app-services-health',
  templateUrl: './services-health.component.html',
  styleUrls: ['./services-health.component.scss'],
  standalone: false,
})
export class ServicesHealthComponent extends PageBaseComponent implements OnInit, OnDestroy {
  tabsData: TabButtonData[] = [];
  entries: ServiceHealthEntry[] = [];
  loading = true;
  error: string | null = null;
  lastUpdated: Date | null = null;

  // Route setup node remote stats.
  rsnStats: RSNRemoteStat[] = [];
  rsnLoading = true;

  private sub: Subscription;
  private rsnSub: Subscription;

  constructor(private healthSvc: ServiceHealthService, private api: ApiService) {
    super();
    this.tabsData = homeTabsData();
  }

  ngOnInit() {
    // Poll every 15s, starting immediately.
    this.sub = interval(15000)
      .pipe(
        startWith(0),
        switchMap(() => this.healthSvc.get()),
      )
      .subscribe({
        next: (entries: ServiceHealthEntry[]) => {
          this.entries = entries || [];
          this.loading = false;
          this.error = null;
          this.lastUpdated = new Date();
        },
        error: (err) => {
          this.loading = false;
          this.error = err?.message || 'Failed to fetch services health';
        },
      });

    // RSN stats poll on a slower cadence — each entry is a DMSG
    // round-trip that the visor caches briefly anyway, and 30s is
    // plenty for an at-a-glance view.
    this.rsnSub = interval(30000)
      .pipe(
        startWith(0),
        switchMap(() => this.api.get('route-setup-nodes/stats')),
      )
      .subscribe({
        next: (rows: RSNRemoteStat[]) => {
          this.rsnStats = Array.isArray(rows) ? rows : [];
          this.rsnLoading = false;
        },
        error: () => { this.rsnLoading = false; },
      });

    return super.ngOnInit();
  }

  ngOnDestroy(): void {
    this.sub?.unsubscribe();
    this.rsnSub?.unsubscribe();
  }

  /** Top 3 failure reasons for one RSN, sorted by count desc. */
  topFailureReasons(snap?: RSNSnapshot): { reason: string, count: number }[] {
    if (!snap?.failures_by_reason) { return []; }
    return Object.entries(snap.failures_by_reason)
      .map(([reason, count]) => ({ reason, count: count as number }))
      .filter((x) => x.count > 0)
      .sort((a, b) => b.count - a.count)
      .slice(0, 3);
  }

  successClass(snap?: RSNSnapshot): string {
    const r = snap?.success_rate_pct ?? 0;
    if (r >= 90) { return 'latency-fast'; }
    if (r >= 70) { return 'latency-medium'; }
    return 'latency-slow';
  }

  trackRSN(_: number, e: RSNRemoteStat): string { return e.pk; }

  /** Map the backend status string to a CSS class for the status dot. */
  statusClass(entry: ServiceHealthEntry): string {
    const s = (entry?.status || '').toUpperCase();
    if (s === 'OK') {
      return 'dot-green';
    }
    if (s === 'DOWN') {
      return 'dot-red';
    }
    return 'dot-outline-gray';
  }

  /** True if any service is DOWN. */
  anyDown(): boolean {
    return this.entries.some((e) => (e?.status || '').toUpperCase() !== 'OK');
  }

  /** Latency formatted with conditional color-coding. */
  latencyClass(entry: ServiceHealthEntry): string {
    const ms = entry?.latency_ms ?? 0;
    if (ms < 500) {
      return 'latency-fast';
    }
    if (ms < 2000) {
      return 'latency-medium';
    }
    return 'latency-slow';
  }

  /** Extract just the host portion of the service URL, for display. */
  shortUrl(url: string): string {
    if (!url) {
      return '';
    }
    try {
      const u = new URL(url);
      return u.host;
    } catch {
      return url;
    }
  }

  trackByName(_: number, e: ServiceHealthEntry): string {
    return e.name;
  }
}
