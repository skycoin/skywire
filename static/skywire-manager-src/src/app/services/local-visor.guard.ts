import { inject } from '@angular/core';
import { CanActivateFn, Router, UrlTree } from '@angular/router';
import { Observable, of } from 'rxjs';
import { catchError, map } from 'rxjs/operators';
import { ApiService } from './api.service';

/**
 * localVisorGuard backs the "Local Visor" home tab / the /nodes/local route: it
 * resolves the hypervisor's OWN visor PK (via /api/about) and redirects to that
 * visor's detail page. Works for both the native hypervisor (the visor serving
 * the UI) and the serverless wasm tab (the in-tab visor). Falls back to the
 * visor list if /api/about can't be reached.
 */
export const localVisorGuard: CanActivateFn = (): Observable<UrlTree> => {
  const api = inject(ApiService);
  const router = inject(Router);

  return api.get('about').pipe(
    map((about: any) => {
      const pk = about && about.public_key;

      return pk ? router.parseUrl('/nodes/' + pk) : router.parseUrl('/nodes/list/1');
    }),
    catchError(() => of(router.parseUrl('/nodes/list/1'))),
  );
};
