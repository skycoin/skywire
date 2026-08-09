import { Injectable } from '@angular/core';
import { Observable, delay, map, of, retryWhen } from 'rxjs';
import { HttpClient } from '@angular/common/http';

/**
 * Ratings some properties of a server can have.
 *
 * TODO: used only for columns that are currently deactivated in the server list. Must
 * be deleted or reactivated depending on what happens to the columns.
 */
/*
export enum Ratings {
  Gold = 0,
  Silver = 1,
  Bronze = 2,
}
*/

/**
 * Data of a server obtained from the discovery service.
 */
export class VpnServer {
  /**
   * 2 letter code of the country the server is in.
   */
  countryCode = 'ZZ';
  /**
   * Sever name.
   */
  name: string;
  /**
   * Location of the server.
   */
  location: string;
  /**
   * Public key.
   */
  pk: string;


  // TODO: used only for columns that are currently deactivated in the server list. Must
  // be deleted or reactivated depending on what happens to the columns.
  /**
   * Current congestion of the server.
   */
   // congestion: number;
  /**
   * Rating of the congestion the server normally has.
   */
  // congestionRating: Ratings;
  /**
   * Latency of the server.
   */
  // latency: number;
  /**
   * Rating of the latency the server normally has.
   */
  // latencyRating: Ratings;
  /**
   * Hops needed for reaching the server.
   */
  // hops: number;


  /**
   * Note the server has in the discovery service.
   */
  note: string;
}

/**
 * Allows to get the vpn servers registered in the discovery service. The service was made for
 * the VPN client.
 *
 * IMPORTANT: changes in the discovery service are needed before being able to get all the data.
 */
@Injectable({
  providedIn: 'root'
})
export class VpnClientDiscoveryService {
  /**
   * Hypervisor route that serves the service discovery from the local CXO
   * subscriber tree (falling back to dmsg-http) — the deployment is dmsg-only, so
   * the legacy clearnet `sd.skycoin.com` frontend is gone. Relative so it targets
   * whichever hypervisor served the UI, and it reuses the same CXO-backed feed the
   * network view uses instead of a public HTTP SD.
   */
  private readonly discoveryServiceUrl = 'api/services?type=vpn';

  /**
   * Servers obtained from the discovery service.
   */
  private servers: VpnServer[];

  constructor(
    private http: HttpClient,
  ) {}

  /**
   * Gets the vpn servers registered in the discovery service. After the first call, it
   * will return the same list in all future calls, to avoid having to make more network
   * connections, until the app is reloaded.
   */
  getServers(): Observable<VpnServer[]> {
    // If a server list was obtained before, return it.
    if (this.servers) {
      return of(this.servers);
    }

    return this.http.get(this.discoveryServiceUrl).pipe(
      // In case of error, retry.
      retryWhen(errors => errors.pipe(delay(4000))),
      map((result: any) => {
        const response: VpnServer[] = [];

        if (result) {
          // The hypervisor's /api/services returns a MAP keyed by PK
          // ({ "<pk>": { pk, services:[...], country } }); the legacy/canonical SD
          // returns an ARRAY ([{ address, geo:{country,region} }]). Support both.
          const entries: any[] = Array.isArray(result) ? result : Object.keys(result).map(k => result[k]);

          entries.forEach(entry => {
            const currentEntry = new VpnServer();

            // Resolve the PK from either shape.
            let pk = '';
            if (entry.address) {
              pk = (entry.address as string).split(':')[0];
            } else if (entry.pk) {
              pk = entry.pk;
            }
            if (!/^[0-9a-fA-F]{66}$/.test(pk)) {
              return;
            }

            // If the entry declares its service types, keep only vpn ones.
            if (Array.isArray(entry.services) && entry.services.length && !entry.services.includes('vpn')) {
              return;
            }

            currentEntry.pk = pk;
            currentEntry.name = pk;

            // Location: map shape has a 2-letter `country`; array shape has `geo`.
            currentEntry.location = '';
            const country = entry.country || (entry.geo && entry.geo.country);
            const region = entry.geo && entry.geo.region;
            if (country) {
              currentEntry.countryCode = country;
            }
            if (region) {
              currentEntry.location = region;
            }

            // TODO: still not added to the discovery service.
            currentEntry.note = '';

            response.push(currentEntry);
          });
        }

        this.servers = response;

        return response;
      })
    );
  }
}
