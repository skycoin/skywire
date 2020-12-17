import { Injectable } from '@angular/core';
import { Observable } from 'rxjs';

import { ApiService } from './api.service';

/**
 * Allows to work with the transports of a node.
 */
@Injectable({
  providedIn: 'root'
})
export class TransportService {
  constructor(
    private apiService: ApiService,
  ) { }

  create(nodeKey: string, remoteKey: string, type: string): Observable<any> {
    return this.apiService.post(`visors/${nodeKey}/transports`, {
      remote_pk: remoteKey,
      transport_type: type,
      public: true,
    });
  }

  delete(nodeKey: string, transportId: string) {
    return this.apiService.delete(`visors/${nodeKey}/transports/${transportId}`);
  }

  /**
   * Gets the list of the transport types the node can work with.
   */
  types(nodeKey: string): Observable<string[]> {
    return this.apiService.get(`visors/${nodeKey}/transport-types`);
  }
}
