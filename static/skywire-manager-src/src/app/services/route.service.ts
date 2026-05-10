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

  /**
   * Sets the minimum number of hops the next routes must have.
   */
  setMinHops(nodeKey: string, minHops: number) {
    const data = {
      min_hops: minHops,
    };

    return this.apiService.post(`visors/${nodeKey}/min-hops`, data);
  }

  /**
   * Lists active route groups on the visor — descriptor (src/dst PK
   * + ports), forward/consume rule IDs, hops, and initiator flag.
   * Maps directly onto the CLI's `skywire cli route groups` /
   * `rg ls`. The HTTP endpoint /visors/<pk>/routegroups is exposed
   * by the hypervisor's getRouteGroups handler.
   */
  routeGroups(nodeKey: string) {
    return this.apiService.get(`visors/${nodeKey}/routegroups`);
  }

  /**
   * Queries the route-finder service via the visor's existing
   * rfClient. body: { src_pk?, dst_pk, min_hops?, max_hops? }.
   * src_pk defaults to the local visor on the backend when empty.
   */
  routeFind(nodeKey: string, body: any) {
    return this.apiService.post(`visors/${nodeKey}/route-find`, body);
  }

  /**
   * Local route enumeration — fetches the TPD transport graph and
   * BFS-walks it for paths from src_pk to dst_pk. Bounded by
   * count (default 200) and max_hops (default 5). Doesn't depend
   * on the route-finder service being reachable.
   */
  routeCalc(nodeKey: string, body: any) {
    return this.apiService.post(`visors/${nodeKey}/route-calc`, body);
  }
}
