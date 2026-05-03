import { Component, OnDestroy, OnInit } from '@angular/core';
import { Subscription, interval, startWith } from 'rxjs';
import { switchMap } from 'rxjs/operators';

import { NodeService } from '../../../services/node.service';
import { TabButtonData } from '../../layout/top-bar/top-bar.component';
import { PageBaseComponent } from 'src/app/utils/page-base';

/**
 * NetworkViewComponent — in-browser equivalent of `skywire cli sd`.
 * Combines service-discovery (proxy/vpn/visor types), transport-
 * discovery (all-transports), and uptime-tracker data into a per-PK
 * table with country/version/services/transport-counts and online
 * status. The aggregation runs server-side on the local visor and
 * is cached for 30s; the page polls at 30s.
 *
 * Color coding mirrors the CLI:
 *   - normal: in UT and reporting "online", with ≥ 2 stcpr/sudph
 *   - amber:  online but fewer than 2 real (stcpr+sudph) transports
 *   - red:    offline in UT
 *   - gray:   not in UT at all
 */

interface NetworkEntry {
  pk: string;
  country?: string;
  version?: string;
  services?: string;
  stcpr: number;
  sudph: number;
  dmsg: number;
  stcp: number;
  total: number;
  ut_status?: string; // "online" | "offline" | "" (unknown)
}

interface NetworkResponse {
  entries: NetworkEntry[];
  fetched_at: string;
}

@Component({
  selector: 'app-network-view',
  templateUrl: './network-view.component.html',
  styleUrls: ['./network-view.component.scss'],
  standalone: false,
})
export class NetworkViewComponent extends PageBaseComponent implements OnInit, OnDestroy {
  tabsData: TabButtonData[] = [];
  entries: NetworkEntry[] = [];
  filteredEntries: NetworkEntry[] = [];
  loading = true;
  error: string | null = null;
  lastUpdated: Date | null = null;

  // Filters
  filterCountry = '';
  filterVersion = '';
  filterMinTransports: number | null = null;
  showOnlineOnly = true;
  searchTerm = '';

  private sub: Subscription;

  constructor(private nodeService: NodeService) {
    super();
    this.tabsData = [
      { icon: 'view_headline', label: 'nodes.title', linkParts: ['/nodes'] },
      { icon: 'monetization_on', label: 'nodes.rewards-title', linkParts: ['/nodes', 'rewards'] },
      { icon: 'health_and_safety', label: 'nodes.services-health-title', linkParts: ['/nodes', 'services-health'] },
      { icon: 'public', label: 'nodes.network-title', linkParts: ['/nodes', 'network'] },
      { icon: 'bubble_chart', label: 'node.details.tpviz.title', linkParts: [], externalUrl: '/tp-viz/' },
      { icon: 'settings', label: 'settings.title', linkParts: ['/settings'] },
    ];
  }

  ngOnInit() {
    // Poll every 5min — the visor caches the aggregation for 5min,
    // so anything finer-grained just hits the cache. The Refresh
    // button below the table forces a fresh fetch when the user
    // wants a current sample on demand.
    this.sub = interval(300000)
      .pipe(
        startWith(0),
        switchMap(() => this.nodeService.getNetworkView()),
      )
      .subscribe({
        next: (resp: NetworkResponse) => this.onResponse(resp),
        error: (err) => {
          this.loading = false;
          this.error = err?.message || 'Failed to fetch network view';
        },
      });
    return super.ngOnInit();
  }

  refreshNow() {
    this.loading = this.entries.length === 0;
    this.nodeService.getNetworkView(true).subscribe({
      next: (resp: NetworkResponse) => this.onResponse(resp),
      error: (err) => {
        this.loading = false;
        this.error = err?.message || 'Failed to fetch network view';
      },
    });
  }

  private onResponse(resp: NetworkResponse) {
    this.entries = resp?.entries || [];
    this.loading = false;
    this.error = null;
    this.lastUpdated = new Date();
    this.applyFilters();
  }

  ngOnDestroy(): void {
    if (this.sub) { this.sub.unsubscribe(); }
  }

  applyFilters() {
    const term = (this.searchTerm || '').trim().toLowerCase();
    const country = (this.filterCountry || '').trim().toUpperCase();
    const version = (this.filterVersion || '').trim();
    const minT = this.filterMinTransports || 0;

    this.filteredEntries = this.entries.filter((e) => {
      if (this.showOnlineOnly && (e.ut_status || '') !== 'online') {
        return false;
      }
      if (country && (e.country || '').toUpperCase() !== country) {
        return false;
      }
      if (version && (e.version || '') !== version) {
        return false;
      }
      if (minT > 0 && (e.total || 0) < minT) {
        return false;
      }
      if (term) {
        const haystack = [e.pk, e.country, e.version, e.services].join(' ').toLowerCase();
        if (!haystack.includes(term)) { return false; }
      }
      return true;
    });
  }

  /** CSS class for a row based on health classification. */
  rowClass(e: NetworkEntry): string {
    const realT = (e.stcpr || 0) + (e.sudph || 0);
    if ((e.ut_status || '') === '') { return 'row-not-in-ut'; }
    if ((e.ut_status || '') === 'offline') { return 'row-offline'; }
    if (realT < 2) { return 'row-low-transports'; }
    return '';
  }

  /** Counts shown in the header for at-a-glance health. */
  get totals() {
    let online = 0, offline = 0, notInUT = 0;
    for (const e of this.entries) {
      const s = e.ut_status || '';
      if (s === 'online') { online++; }
      else if (s === 'offline') { offline++; }
      else { notInUT++; }
    }
    return { all: this.entries.length, online, offline, notInUT };
  }
}
