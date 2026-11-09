import { Injectable } from '@angular/core';
import { Observable } from 'rxjs';

import { ApiService } from './api.service';

/** The relay. Mirrors pkg/alerts.SMTP; reads never carry the password. */
export interface AlertsSmtp {
  host: string;
  port?: number;
  username?: string;
  password?: string;
  starttls?: boolean;
  password_set?: boolean;
}

/** Mirrors pkg/alerts.Settings. offline_after is a duration like "5m0s". */
export interface AlertsSettings {
  email_to?: string[] | null;
  offline_after?: string;
  smtp?: AlertsSmtp | null;
  webhook?: string;
  watch?: string[] | null;
  from?: string;
}

export interface AlertsWatched {
  pk: string;
  hostname?: string;
  public_ip?: string;
  last_seen?: string;
  online: boolean;
  source?: string;
  offline_since?: string;
  alerted?: boolean;
}

export interface AlertsResult {
  channel: string;
  target: string;
  error?: string;
}

/** Mirrors pkg/alerts.Status. */
export interface AlertsStatus {
  settings: AlertsSettings;
  enabled: boolean;
  offline_after: string;
  watched: AlertsWatched[] | null;
  last_sent?: string;
  last_results?: AlertsResult[] | null;
  pending?: number;
}

/** The offline delay choices, all within the visor's 1m to 1h range. */
export const alertDelays = [
  { value: '1m0s', label: '1 minute' },
  { value: '5m0s', label: '5 minutes' },
  { value: '15m0s', label: '15 minutes' },
  { value: '30m0s', label: '30 minutes' },
  { value: '1h0m0s', label: '1 hour' },
];

/**
 * A visor's offline alerts, through
 *   GET  /api/visors/<pk>/alerts
 *   PUT  /api/visors/<pk>/alerts
 *   POST /api/visors/<pk>/alerts/test
 * the same calls `skywire cli visor alerts` makes over RPC.
 */
@Injectable({
  providedIn: 'root',
})
export class AlertsService {
  constructor(private apiService: ApiService) {}

  get(pk: string): Observable<AlertsStatus> {
    return this.apiService.get(`visors/${pk}/alerts`) as Observable<AlertsStatus>;
  }

  set(pk: string, s: AlertsSettings): Observable<AlertsStatus> {
    return this.apiService.put(`visors/${pk}/alerts`, s) as Observable<AlertsStatus>;
  }

  test(pk: string): Observable<AlertsResult[]> {
    return this.apiService.post(`visors/${pk}/alerts/test`) as Observable<AlertsResult[]>;
  }

  /** Drops an offline visor from the hypervisor's remembered list. */
  forget(pk: string): Observable<any> {
    return this.apiService.delete(`visors/${pk}/known`);
  }
}
