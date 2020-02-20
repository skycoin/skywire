import { Injectable } from '@angular/core';
import { Observable } from 'rxjs';

import { ApiService } from './api.service';
import { Route } from '../app.datatypes';

/**
 * Allows to work with the routes of a node.
 */
@Injectable({
  providedIn: 'root'
})
export class RouteService {
  constructor(
    private apiService: ApiService,
  ) { }

  /**
   * Get a list with the routes of a node.
   */
  getRoutes(nodeKey: string): Observable<Route[]> {
    return this.apiService.get(`visors/${nodeKey}/routes`);
  }

  /**
   * Gets the details of a specific route.
   */
  get(nodeKey: string, routeId: string) {
    return this.apiService.get(`visors/${nodeKey}/routes/${routeId}`);
  }

  delete(nodeKey: string, routeId: string) {
    return this.apiService.delete(`visors/${nodeKey}/routes/${routeId}`);
  }
}
