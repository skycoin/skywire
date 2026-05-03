import { Component, OnDestroy, OnInit } from '@angular/core';
import { Subscription, interval, startWith } from 'rxjs';
import { switchMap } from 'rxjs/operators';

import { ServiceHealthEntry, ServiceHealthService } from 'src/app/services/service-health.service';
import { TabButtonData } from '../../layout/top-bar/top-bar.component';
import { PageBaseComponent } from 'src/app/utils/page-base';

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

  private sub: Subscription;

  constructor(private healthSvc: ServiceHealthService) {
    super();
    this.tabsData = [
      {
        icon: 'view_headline',
        label: 'nodes.title',
        linkParts: ['/nodes'],
      },
      {
        icon: 'monetization_on',
        label: 'nodes.rewards-title',
        linkParts: ['/nodes', 'rewards'],
      },
      {
        icon: 'health_and_safety',
        label: 'nodes.services-health-title',
        linkParts: ['/nodes', 'services-health'],
      },
      {
        icon: 'public',
        label: 'nodes.network-title',
        linkParts: ['/nodes', 'network'],
      },
      {
        icon: 'bubble_chart',
        label: 'node.details.tpviz.title',
        linkParts: [],
        externalUrl: '/tp-viz/',
      },
      {
        icon: 'settings',
        label: 'settings.title',
        linkParts: ['/settings'],
      },
    ];
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
    return super.ngOnInit();
  }

  ngOnDestroy(): void {
    if (this.sub) {
      this.sub.unsubscribe();
    }
  }

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
