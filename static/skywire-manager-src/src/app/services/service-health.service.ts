import { Injectable } from '@angular/core';
import { Observable } from 'rxjs';

import { ApiService } from './api.service';

/**
 * Health status of a single deployment service, as returned by
 * /api/service-health.
 *
 * Mirrors pkg/visor.ServiceHealthEntry on the backend.
 */
export interface ServiceHealthEntry {
  name: string;
  url: string;
  status: string;
  latency_ms: number;
  version?: string;
}

/**
 * Fetches the aggregated health of all configured deployment services
 * (TPD, DMSG discovery, AR, RF, UT, SD). The backend queries each
 * service's /health endpoint via the visor's DMSG client when available,
 * falling back to direct HTTP.
 */
@Injectable({
  providedIn: 'root',
})
export class ServiceHealthService {
  constructor(private apiService: ApiService) {}

  /** One-shot fetch of all deployment services. */
  get(): Observable<ServiceHealthEntry[]> {
    return this.apiService.get('service-health') as Observable<ServiceHealthEntry[]>;
  }
}
