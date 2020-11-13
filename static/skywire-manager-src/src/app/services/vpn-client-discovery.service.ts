import { Injectable } from '@angular/core';
import { Observable } from 'rxjs';
import { HttpClient } from '@angular/common/http';
import { retryWhen, delay, map } from 'rxjs/operators';

export enum Ratings {
  Gold = 0,
  Silver = 1,
  Bronze = 2,
}

export class VpnServer {
  countryCode: string;
  name: string;
  location: string;
  pk: string;
  congestion: number;
  congestionRating: Ratings;
  latency: number;
  latencyRating: Ratings;
  hops: number;
  note: string;
}

/**
 * Allows to get the vpn servers registered in the discovery service.
 *
 * IMPORTANT: changes in the discovery service are needed before being able to get all the data.
 */
@Injectable({
  providedIn: 'root'
})
export class VpnClientDiscoveryService {
  /**
   * URL of the discovery service.
   */
  private readonly discoveryServiceUrl = 'https://service.discovery.skycoin.com/api/services?type=vpn';

  constructor(
    private http: HttpClient,
  ) {}

  /**
   * Gets the vpn servers registered in the discovery service.
   */
  getServers(): Observable<VpnServer[]> {
    const response: VpnServer[] = [];

    return this.http.get(this.discoveryServiceUrl).pipe(
      // In case of error, retry.
      retryWhen(errors => errors.pipe(delay(4000))),
      map((result: any[]) => {
        // Process the data.
        result.forEach(entry => {
          const currentEntry = new VpnServer();

          // The address must have 2 parts: the pk and the port.
          const addressParts = (entry.address as string).split(':');
          if (addressParts.length === 2) {
            currentEntry.pk = addressParts[0];

            // Process the location.
            currentEntry.location = '';
            if (entry.geo) {
              if (entry.geo.country) {
                currentEntry.countryCode = entry.geo.country;
              }
              if (entry.geo.region) {
                currentEntry.location = entry.geo.region;
              }
            }

            // Data that must be obtained after the changes in the service.
            currentEntry.name = addressParts[0];
            currentEntry.congestion = 20;
            currentEntry.congestionRating = Ratings.Gold;
            currentEntry.latency = 123;
            currentEntry.latencyRating = Ratings.Gold;
            currentEntry.hops = 3;
            currentEntry.note = '';

            response.push(currentEntry);
          }
        });

        return response;
      })
    );
  }
}
