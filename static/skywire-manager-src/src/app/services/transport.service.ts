import { Injectable } from '@angular/core';
import { Observable } from 'rxjs';
import { PersistentTransport } from '../app.datatypes';

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
   * Rewrites the list of persistent transports.
   */
  savePersistentTransportsData(nodeKey: string, newData: PersistentTransport[]) {
    return this.apiService.put(`visors/${nodeKey}/persistent-transports`, newData);
  }

  /**
   * Gets the persistent transports list
   */
  getPersistentTransports(nodeKey: string) {
    return this.apiService.get(`visors/${nodeKey}/persistent-transports`);
  }

  /**
   * Gets the list of the transport types the node can work with.
   */
  types(nodeKey: string): Observable<string[]> {
    return this.apiService.get(`visors/${nodeKey}/transport-types`);
  }
}
