import { inject } from '@angular/core';
import { ActivatedRouteSnapshot, CanActivateFn, Router, UrlTree } from '@angular/router';
import { Observable, of } from 'rxjs';
import { catchError, map } from 'rxjs/operators';
import { ApiService } from './api.service';

/**
 * localVisorGuard backs the "Local Visor" home tab / the /nodes/local route: it
 * resolves the hypervisor's OWN visor PK (via /api/about) and redirects to that
 * visor's detail page. Works for both the native hypervisor (the visor serving
 * the UI) and the serverless wasm tab (the in-tab visor). Falls back to the
 * visor list if /api/about can't be reached. /nodes/local/<tab> lands on that tab.
 */
export const localVisorGuard: CanActivateFn = (route: ActivatedRouteSnapshot): Observable<UrlTree> => {
  const api = inject(ApiService);
  const router = inject(Router);
  const tab = route.paramMap.get('tab');

  return api.get('about').pipe(
    map((about: any) => {
      const pk = about && about.public_key;
      if (!pk) {
        return router.parseUrl('/nodes/list/1');
      }

      const commands = tab ? ['/nodes', pk, tab] : ['/nodes', pk];

      return router.createUrlTree(commands, { queryParams: route.queryParams });
    }),
    catchError(() => of(router.parseUrl('/nodes/list/1'))),
  );
};
