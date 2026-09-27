import { Injectable } from '@angular/core';
import { Observable } from 'rxjs';

import { ApiService } from './api.service';

/**
 * One `skywire autoconfig` flag and the skywire.conf variable it writes.
 * Mirrors pkg/skywireconfig/autoconfigcmd.Flag.
 */
export interface SkyenvFlag {
  name: string;
  short?: string;
  type: string;
  default?: string;
  description: string;
  env_key?: string;
  env_format?: string;
  env_negate?: boolean;
  env_default?: string;
  env_note?: string;
}

/**
 * The visor's /etc/skywire.conf. Mirrors pkg/visor/visorapi.SkyenvState.
 * values holds the active assignments only; secrets read "(set)".
 */
export interface SkyenvState {
  path: string;
  exists: boolean;
  writable: boolean;
  values: { [key: string]: string } | null;
  flags: SkyenvFlag[];
}

/**
 * One edit request, addressed by flag name. Mirrors visorapi.SkyenvEdits.
 */
export interface SkyenvEdits {
  set?: { [flag: string]: string };
  unset?: string[];
}

/** What Skyenv reports in place of a secret's value. */
export const skyenvRedacted = '(set)';

/**
 * Reads and edits a visor's skywire.conf through
 *   GET /api/visors/<pk>/skyenv
 *   PUT /api/visors/<pk>/skyenv
 * the same calls `skywire cli visor skyenv` makes.
 */
@Injectable({
  providedIn: 'root',
})
export class SkyenvService {
  constructor(private apiService: ApiService) {}

  get(pk: string): Observable<SkyenvState> {
    return this.apiService.get(`visors/${pk}/skyenv`) as Observable<SkyenvState>;
  }

  set(pk: string, edits: SkyenvEdits): Observable<SkyenvState> {
    return this.apiService.put(`visors/${pk}/skyenv`, edits) as Observable<SkyenvState>;
  }
}
