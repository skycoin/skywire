import { Injectable } from '@angular/core';
import { Observable } from 'rxjs';
import { HttpClient } from '@angular/common/http';
import { retryWhen, delay, map } from 'rxjs/operators';

import { ProxyDiscoveryEntry } from '../app.datatypes';
import { environment } from 'src/environments/environment';

/**
 * Allows to get the proxies registered in the proxy discovery service.
 */
@Injectable({
  providedIn: 'root'
})
export class ProxyDiscoveryService {
  /**
   * URL of the proxy discovery service. While in dev mode the url is managed by the
   * dev server proxy.
   */
  private readonly discoveryServiceUrl = 'https://service.discovery.skycoin.com/api/services?type=proxy';

  constructor(
    private http: HttpClient,
  ) {}

  /**
   * Get the proxies registered in the proxy discovery service.
   */
  getProxies(): Observable<ProxyDiscoveryEntry[]> {
    const response: ProxyDiscoveryEntry[] = [];

    return this.http.get(this.discoveryServiceUrl).pipe(
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
              if (proxy.geo.region) {
                currentEntry.region = proxy.geo.region;
                currentEntry.location += currentEntry.region;
              }

              if (proxy.geo.region && proxy.geo.country) {
                currentEntry.location += ', ';
              }

              if (proxy.geo.country) {
                currentEntry.country = proxy.geo.country;
                currentEntry.location += currentEntry.country;
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
