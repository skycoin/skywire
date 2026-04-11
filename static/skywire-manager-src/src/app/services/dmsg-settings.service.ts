import { Injectable } from '@angular/core';
import { Observable } from 'rxjs';

import { ApiService } from './api.service';

/**
 * One dmsg client's current session state. Mirrors pkg/visor.DmsgClientSessionInfo.
 */
export interface DmsgClientSessionInfo {
  pk: string;
  role: 'main' | 'route_setup' | 'transport_setup';
  count: number;
  servers: string[];
}

/**
 * The set of dmsg clients a visor is running. A visor may host up to three
 * dmsg clients, each with its own public key and its own session set:
 * the main visor dmsg client, the embedded route setup-node's client,
 * and the embedded transport setup-node's client.
 *
 * Mirrors pkg/visor.DmsgClientSessions.
 */
export interface DmsgClientSessions {
  main?: DmsgClientSessionInfo;
  route_setup?: DmsgClientSessionInfo;
  transport_setup?: DmsgClientSessionInfo;
}

/**
 * Result of a DmsgConnectAll or SetDmsgSessionsCount call.
 * Mirrors pkg/visor.DmsgConnectAllResult.
 */
export interface DmsgConnectAllResult {
  total: number;
  already_connected: number;
  newly_connected: number;
  failed?: { [pk: string]: string };
}

/**
 * Client for the visor's dmsg settings endpoints:
 *   GET  /api/dmsg/sessions         — per-client session state
 *   POST /api/dmsg/connect-all      — one-shot "reach every server now"
 *   PUT  /api/dmsg/sessions-count   — persist sessions_count + connect-all
 */
@Injectable({
  providedIn: 'root',
})
export class DmsgSettingsService {
  constructor(private apiService: ApiService) {}

  /** One-shot fetch of per-client session state. */
  getSessions(): Observable<DmsgClientSessions> {
    return this.apiService.get('dmsg/sessions') as Observable<DmsgClientSessions>;
  }

  /** One-shot action: open a dmsg session to every known server. */
  connectAll(): Observable<DmsgConnectAllResult> {
    return this.apiService.post('dmsg/connect-all') as Observable<DmsgConnectAllResult>;
  }

  /**
   * Persist dmsg.sessions_count to the visor config file (so it survives
   * restart) and trigger an immediate connect-all. A value of 0 means
   * "connect to every available dmsg server" — recommended for
   * visors hosting the embedded route or transport setup-node.
   */
  setSessionsCount(count: number): Observable<DmsgConnectAllResult> {
    return this.apiService.put('dmsg/sessions-count', { count }) as Observable<DmsgConnectAllResult>;
  }
}
