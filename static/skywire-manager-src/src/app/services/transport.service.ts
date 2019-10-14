import { Injectable } from '@angular/core';
import { ApiService } from './api.service';
import { Observable } from 'rxjs';
import { map } from 'rxjs/operators';
import { Transport } from '../app.datatypes';

@Injectable({
  providedIn: 'root'
})
export class TransportService {
  constructor(
    private apiService: ApiService,
  ) { }

  getTransports(nodeKey: string): Observable<Transport[]> {
    return this.apiService.get(`visors/${nodeKey}/transports`, { api2: true }).pipe(map((response: Transport[]) => {
      return response.sort((a, b) => a.remote_pk.localeCompare(b.remote_pk));
    }));
  }

  create(key: string, remoteKey: string, type: string) {
    return this.apiService.post(`visors/${key}/transports`, {
      remote_pk: remoteKey,
      transport_type: type,
      public: true,
    }, {
      api2: true,
      type: 'json',
    });
  }

  delete(key: string, transportId: string) {
    return this.apiService.delete(`visors/${key}/transports/${transportId}`, {api2: true});
  }

  types(key: string): Observable<string[]> {
    return this.apiService.get(`visors/${key}/transport-types`, {api2: true});
  }
}
