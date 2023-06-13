import { Injectable } from '@angular/core';
import { Observable } from 'rxjs';

import { ApiService } from './api.service';

/**
 * Allows to work with the ports of a node.
 */
@Injectable({
  providedIn: 'root'
})
export class FwdService {
  constructor(
    private apiService: ApiService,
  ) { }

  /**
   * Creates a connection to a remote port.
   */
  createRemote(nodeKey: string, remotePk: string, remotePort: number, localPort: number): Observable<any> {
    const data = {
      remote_pk: remotePk,
      remote_port: remotePort,
      local_port: localPort,
    };

    return this.apiService.post(`visors/${nodeKey}/rev`, data);
  }

  /**
   * Deletes a connection to a remote port.
   */
  deleteRemote(nodeKey: string, connectionID: string): Observable<any> {
    return this.apiService.delete(`visors/${nodeKey}/rev/` + connectionID);
  }

  /**
   * Opens a local port, to share it.
   */
  createLocal(nodeKey: string, localPort: number): Observable<any> {
    const data = {
      port: localPort,
    };

    return this.apiService.post(`visors/${nodeKey}/fwd`, data);
  }

  /**
   * Removes a local port.
   */
  deleteLocal(nodeKey: string, port: string): Observable<any> {
    return this.apiService.delete(`visors/${nodeKey}/fwd/` + port);
  }
}
