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

  /**
   * Creates a transport.
   * @param nodeKey Public key of the local node.
   * @param remoteKey Public key of the remote node.
   * @param type Transport type.
   */
  create(nodeKey: string, remoteKey: string, type: string): Observable<any> {
    const data = {
      remote_pk: remoteKey,
      public: true,
    };

    if (type) {
      data['transport_type'] = type;
    }

    return this.apiService.post(`visors/${nodeKey}/transports`, data);
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
