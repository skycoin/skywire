import { Injectable } from '@angular/core';
import { Observable } from 'rxjs';

import { ApiService } from './api.service';
import { Transport } from '../app.datatypes';

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

  /**
   * Get a list with the transports of a node.
   */
  getTransports(nodeKey: string): Observable<Transport[]> {
    return this.apiService.get(`nodes/${nodeKey}/transports`);
  }

  create(nodeKey: string, remoteKey: string, type: string): Observable<any> {
    return this.apiService.post(`nodes/${nodeKey}/transports`, {
      remote_pk: remoteKey,
      transport_type: type,
      public: true,
    });
  }

  delete(nodeKey: string, transportId: string) {
    return this.apiService.delete(`nodes/${nodeKey}/transports/${transportId}`);
  }

  /**
   * Gets the list of the transport types the node can work with.
   */
  types(nodeKey: string): Observable<string[]> {
    return this.apiService.get(`nodes/${nodeKey}/transport-types`);
  }
}
