import { Injectable } from '@angular/core';
import { Observable, delay, map, retryWhen } from 'rxjs';
import { HttpClient } from '@angular/common/http';

import { ProxyDiscoveryEntry } from '../app.datatypes';
import { countriesList } from '../utils/countries-list';

/**
 * Lists the proxy (skysocks) and VPN servers registered in the service discovery.
 *
 * Data source: the hypervisor's own `/api/services` route, which resolves the
 * service discovery over dmsg (the deployment is dmsg-only — the legacy clearnet
 * `sd.skycoin.com` frontend is gone). Using a relative URL means it works whether
 * the UI is served by a native hypervisor or an hv-served wasm visor, and it
 * inherits the visor's dmsg connectivity instead of needing a public HTTP SD.
 */
@Injectable({
  providedIn: 'root'
})
export class ProxyDiscoveryService {
  /**
   * Hypervisor route that forwards to the service discovery over dmsg. Relative so
   * it targets whichever hypervisor served the UI.
   */
  private readonly discoveryServiceUrl = 'api/services?type=';

  constructor(
    private http: HttpClient,
  ) {}

  /**
   * Get the proxies or vpn servers registered in the discovery service.
   * @param getProxies If true, get proxies (skysocks); if false, get vpn servers.
   */
  getServices(getProxies: boolean): Observable<ProxyDiscoveryEntry[]> {
    const wantType = getProxies ? 'proxy' : 'vpn';

    return this.http.get(this.discoveryServiceUrl + wantType).pipe(
      // In case of error, retry.
      retryWhen(errors => errors.pipe(delay(4000))),
      map((result: any) => {
        const response: ProxyDiscoveryEntry[] = [];
        if (!result) {
          return response;
        }

        // The hypervisor's /api/services returns a MAP keyed by PK
        // ({ "<pk>": { pk, services:[...], country } }); the legacy/canonical SD
        // returns an ARRAY ([{ address, geo:{country,region}, ... }]). Support both.
        const entries: any[] = Array.isArray(result) ? result : Object.keys(result).map(k => result[k]);

        entries.forEach(raw => {
          const currentEntry = new ProxyDiscoveryEntry();

          // Resolve the PK + address from either shape.
          let pk = '';
          if (raw.address) {
            const parts = (raw.address as string).split(':');
            pk = parts[0];
            currentEntry.address = raw.address;
            currentEntry.port = parts.length === 2 ? parts[1] : '';
          } else if (raw.pk) {
            pk = raw.pk;
            currentEntry.address = raw.pk;
          }
          if (!/^[0-9a-fA-F]{66}$/.test(pk)) {
            return;
          }
          currentEntry.pk = pk;

          // If the entry declares its service types, keep only the requested one
          // (the map shape carries a `services` array); tolerate entries without.
          if (Array.isArray(raw.services) && raw.services.length && !raw.services.includes(wantType)) {
            return;
          }

          // Location: map shape has a 2-letter `country`; array shape has `geo`.
          currentEntry.location = '';
          const country = raw.country || (raw.geo && raw.geo.country);
          const region = raw.geo && raw.geo.region;
          if (country) {
            currentEntry.country = country;
            currentEntry.location += (countriesList as any)[String(country).toUpperCase()] || country;
          }
          if (region) {
            currentEntry.region = region;
            currentEntry.location += (country ? ', ' : '') + region;
          }

          response.push(currentEntry);
        });

        return response;
      })
    );
  }
}
