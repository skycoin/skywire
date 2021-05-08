import { Injectable } from '@angular/core';
import { Observable } from 'rxjs';
import { HttpClient } from '@angular/common/http';
import { retryWhen, delay, map } from 'rxjs/operators';

import { ProxyDiscoveryEntry } from '../app.datatypes';
import { countriesList } from '../utils/countries-list';

/**
 * Allows to get the proxies and vpn servers registered in the discovery service.
 */
@Injectable({
  providedIn: 'root'
})
export class ProxyDiscoveryService {
  /**
   * URL of the discovery service.
   */
  private readonly discoveryServiceUrl = 'https://service.discovery.skycoin.com/api/services?type=';

  constructor(
    private http: HttpClient,
  ) {}

  /**
   * Get the proxies or vpn servers registered in the discovery service.
   * @param getProxies If true, the function will get the proxies. If false, the function
   * will get vpn servers.
   */
  getServices(getProxies: boolean): Observable<ProxyDiscoveryEntry[]> {
    const response: ProxyDiscoveryEntry[] = [];

    return this.http.get(this.discoveryServiceUrl + (getProxies ? 'proxy' : 'vpn')).pipe(
      // In case of error, retry.
      retryWhen(errors => errors.pipe(delay(4000))),
      map((result: any[]) => {
        // Process the data.
        result.forEach(proxy => {
          const currentEntry = new ProxyDiscoveryEntry();

          // The address must have 2 parts: the pk and the port.
          const addressParts = (proxy.address as string).split(':');
          if (addressParts.length === 2) {
            currentEntry.address = proxy.address;
            currentEntry.pk = addressParts[0];
            currentEntry.port = addressParts[1];

            currentEntry.location = '';

            // Process the location.
            if (proxy.geo) {
              if (proxy.geo.country) {
                currentEntry.country = proxy.geo.country;
                currentEntry.location += countriesList[proxy.geo.country.toUpperCase()] ?
                  countriesList[proxy.geo.country.toUpperCase()] :
                  proxy.geo.country;
              }

              if (proxy.geo.region && proxy.geo.country) {
                currentEntry.location += ', ';
              }

              if (proxy.geo.region) {
                currentEntry.region = proxy.geo.region;
                currentEntry.location += currentEntry.region;
              }
            }

            response.push(currentEntry);
          }
        });

        return response;
      })
    );
  }
}
