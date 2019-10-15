import { Injectable } from '@angular/core';
import { ApiService } from './api.service';
import { Observable } from 'rxjs';
import { Route } from '../app.datatypes';
import { map } from 'rxjs/operators';

@Injectable({
  providedIn: 'root'
})
export class RouteService {
  constructor(
    private apiService: ApiService,
  ) { }

  getRoutes(nodeKey: string): Observable<Route[]> {
    return this.apiService.get(`visors/${nodeKey}/routes`, { api2: true }).pipe(map((response: Route[]) => {
      if (response) {
        response = response.sort((a, b) => a.key - b.key);
      }

      return response;
    }));
  }

  get(key: string, routeId: string) {
    return this.apiService.get(`visors/${key}/routes/${routeId}`, {api2: true});
  }

  delete(key: string, routeId: string) {
    return this.apiService.delete(`visors/${key}/routes/${routeId}`, {api2: true});
  }
}
