import { Injectable } from '@angular/core';
import { ApiService } from './api.service';
import { Observable } from 'rxjs';
import { ClientConnection } from '../app.datatypes';

// This file appears to be only needed by an unnused component: HistoryComponent.
// May not work anymore due to the change from testnet to mainnet, but if it does it
// may currently have problems due to the fact that the apiService.post function now
// adds "/api" as part of the request made to the backend.

@Injectable({
  providedIn: 'root'
})
export class ClientConnectionService {
  constructor(
    private apiService: ApiService,
  ) { }

  get(key: string): Observable<ClientConnection[]|null> {
    return this.request('conn/getClientConnection', key);
  }

  save(key: string, data: object) {
    return this.request('conn/saveClientConnection', key, {data: JSON.stringify(data)});
  }

  edit(key: string, index: number, label: string) {
    return this.request('conn/editClientConnection', key, {index, label});
  }

  remove(key: string, index: number) {
    return this.request('conn/removeClientConnection', key, {index});
  }

  private request(endpoint: string, key: string, data?: object) {
    return this.apiService.post(endpoint, {
      client: key,
      ...data,
    });
  }
}
