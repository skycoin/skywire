import { Injectable } from '@angular/core';
import { HttpClient } from '@angular/common/http';
import { Observable, forkJoin, of, map, catchError } from 'rxjs';

/**
 * A single day's reward entry for one visor, from the daily history endpoint.
 */
export interface RewardHistoryEntry {
  pk: string;
  'reward-address': string;
  amount: number;
  share: number;
}

/**
 * Reward data for a single visor, aggregated across multiple days.
 */
export interface VisorRewardData {
  pk: string;
  rewardAddress: string;
  /** Map of date string (YYYY-MM-DD) to amount */
  dailyAmounts: { [date: string]: number };
  /** Map of date string to whether it was sent */
  dailySent: { [date: string]: boolean };
  weekTotal: number;
}

/**
 * Service for fetching reward data from the reward system.
 */
@Injectable({
  providedIn: 'root'
})
export class RewardService {
  // Reward API is proxied through the hypervisor to enable DMSG-first access.
  // The hypervisor routes: /api/rewards/* -> reward system (DMSG then HTTP fallback).
  private readonly rewardSystemUrl = '/api/rewards';

  /** Cache of fetched reward data, keyed by visor PK */
  private rewardDataCache: Map<string, VisorRewardData> = new Map();
  /** The dates that were fetched for the cache */
  private cachedDates: string[] = [];
  /** Whether a fetch is currently in progress */
  fetching = false;

  constructor(private http: HttpClient) {}

  /**
   * Returns the last N days as YYYY-MM-DD strings, most recent first.
   */
  getLastNDays(n: number): string[] {
    const dates: string[] = [];
    for (let i = 0; i < n; i++) {
      const d = new Date();
      d.setDate(d.getDate() - i);
      dates.push(d.toISOString().split('T')[0]);
    }
    return dates;
  }

  /**
   * Formats a date string (YYYY-MM-DD) to short display format (e.g. "Apr 5").
   */
  formatDateShort(dateStr: string): string {
    const d = new Date(dateStr + 'T00:00:00');
    const months = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];
    return months[d.getMonth()] + ' ' + d.getDate();
  }

  /**
   * Returns cached reward data if available.
   */
  getCachedData(): Map<string, VisorRewardData> {
    return this.rewardDataCache;
  }

  /**
   * Returns the dates that were fetched.
   */
  getCachedDates(): string[] {
    return this.cachedDates;
  }

  /**
   * Returns true if we have cached data.
   */
  hasCache(): boolean {
    return this.cachedDates.length > 0;
  }

  /**
   * Fetches reward history for the last 7 days using the per-day endpoint.
   * Each day fetches: GET <reward_system>/skycoin-rewards/hist/<date>/json
   * which returns an array of { pk, "reward-address", amount, share } entries.
   *
   * Results are cached so switching tabs doesn't re-fetch.
   */
  fetchRewardData(visorPks: string[]): Observable<Map<string, VisorRewardData>> {
    // If we already have cached data, return it
    if (this.hasCache()) {
      return of(this.rewardDataCache);
    }

    this.fetching = true;

    // Fetch per-visor reward history (last 7 days) using the per-PK endpoint.
    // This is much more efficient than fetching all rewards for all visors.
    const requests: Observable<{ pk: string; history: any[] }>[] = visorPks.map(pk => {
      return this.http.get<any>(
        `${this.rewardSystemUrl}/skycoin-rewards/visor/${pk}?days=7`
      ).pipe(
        map(resp => ({ pk, history: (resp && resp.history ? resp.history : []) })),
        catchError(() => of({ pk, history: [] as any[] }))
      );
    });

    return forkJoin(requests).pipe(
      map(results => {
        const dataMap = new Map<string, VisorRewardData>();

        for (const { pk, history } of results) {
          const visorData: VisorRewardData = {
            pk,
            rewardAddress: '',
            dailyAmounts: {},
            dailySent: {},
            weekTotal: 0,
          };

          for (const day of history) {
            if (day.date) {
              visorData.dailyAmounts[day.date] = day.amount || 0;
              visorData.dailySent[day.date] = day.sent || false;
              visorData.weekTotal += day.amount || 0;
            }
          }

          dataMap.set(pk, visorData);
        }

        // Cache results
        this.rewardDataCache = dataMap;
        this.cachedDates = this.getLastNDays(7);
        this.fetching = false;

        return dataMap;
      }),
      catchError(err => {
        this.fetching = false;
        return of(new Map<string, VisorRewardData>());
      })
    );
  }

  /**
   * Clears the cache so the next fetch will re-request data.
   */
  clearCache(): void {
    this.rewardDataCache = new Map();
    this.cachedDates = [];
  }
}
