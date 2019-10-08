import { Injectable } from '@angular/core';
import { ApiService } from './api.service';
import { Observable } from 'rxjs';

@Injectable({
  providedIn: 'root'
})
export class TransportService {
  constructor(
    private apiService: ApiService,
  ) { }

  create(key: string, remoteKey: string, type: string) {
    return this.apiService.post(`nodes/${key}/transports`, {
      remote_pk: remoteKey,
      transport_type: type,
      public: true,
    }, {
      api2: true,
      type: 'json',
    });
  }

  delete(key: string, transportId: string) {
    return this.apiService.delete(`nodes/${key}/transports/${transportId}`, {api2: true});
  }

  types(key: string): Observable<string[]> {
    return this.apiService.get(`nodes/${key}/transport-types`, {api2: true});
  }
}
