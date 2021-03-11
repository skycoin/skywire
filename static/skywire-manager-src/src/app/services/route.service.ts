import { Injectable } from '@angular/core';

import { ApiService } from './api.service';

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
   * Gets the details of a specific route.
   */
  get(nodeKey: string, routeId: string) {
    return this.apiService.get(`visors/${nodeKey}/routes/${routeId}`);
  }

  delete(nodeKey: string, routeId: string) {
    return this.apiService.delete(`visors/${nodeKey}/routes/${routeId}`);
  }
}
