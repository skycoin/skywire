import { Injectable } from '@angular/core';
import { Observable } from 'rxjs';
import { HttpClient } from '@angular/common/http';
import { retryWhen, delay, map } from 'rxjs/operators';

import { ProxyDiscoveryEntry } from '../app.datatypes';

/**
 * Allows to get the proxies registered in the proxy discovery service.
 */
@Injectable({
  providedIn: 'root'
})
export class ProxyDiscoveryService {
  /**
   * URL of the proxy discovery service.
   */
  private readonly discoveryServiceUrl = 'http://localhost:8081';

  constructor(
    private http: HttpClient,
  ) {}

  /**
   * Get the proxies registered in the proxy discovery service.
   */
  getProxies(): Observable<ProxyDiscoveryEntry[]> {
    return this.http.get(this.discoveryServiceUrl + '/api/v1/getAll').pipe(
      // In case of error, retry.
      retryWhen(errors => errors.pipe(delay(4000))),
      map((response: ProxyDiscoveryEntry[]) => {
        // Process the data.
        response.forEach(proxy => {
          // Remove the invalid dates.
          if (proxy.updatedAt) {
            proxy.updatedAt = proxy.updatedAt.startsWith('0001-01-01') ? null : proxy.updatedAt;
          }

          // Process the status.
          if (proxy.status) {
            proxy.available = proxy.status.toLowerCase() === 'available';
          }

          // Process the location.
          let location = '';
          if (proxy.city) {
            location += proxy.city;
          }
          if (proxy.city && proxy.country) {
            location += ', ';
          }
          if (proxy.country) {
            location += proxy.country;
          }
          proxy.location = location;
        });

        return response;
      })
    );
  }
}
