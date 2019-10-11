import { Injectable } from '@angular/core';
import { ApiService } from './api.service';
import { Observable } from 'rxjs';

@Injectable({
  providedIn: 'root'
})
export class RouteService {
  constructor(
    private apiService: ApiService,
  ) { }

  get(key: string, routeId: string) {
    return this.apiService.get(`visors/${key}/routes/${routeId}`, {api2: true});
  }

  delete(key: string, routeId: string) {
    return this.apiService.delete(`visors/${key}/routes/${routeId}`, {api2: true});
  }
}
