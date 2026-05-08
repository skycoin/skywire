import { RouteReuseStrategy, ActivatedRouteSnapshot, DetachedRouteHandle } from '@angular/router';
import { Injectable } from '@angular/core';

/**
 * Default Angular route reuse behavior, made explicit. Same-config
 * routes are reused (which is what keeps NodeComponent alive while
 * the user clicks between per-visor tabs); nothing is detached or
 * cached.
 *
 * The earlier custom strategy tried to cache the Terminal route's
 * view so the dmsgpty iframe would survive tab switches, but
 * Angular's detach/attach mechanism removes the cached view's DOM
 * from the document — and browsers reload an iframe whenever its
 * element is detached and re-attached, so the cache could never
 * actually preserve the running shell session. The iframe now lives
 * directly in NodeComponent's template (rendered as a sibling of the
 * router-outlet, hidden when off-tab), which keeps it parked in the
 * DOM continuously.
 */
@Injectable()
export class AppReuseStrategy implements RouteReuseStrategy {
  shouldDetach(_route: ActivatedRouteSnapshot): boolean { return false; }
  store(_route: ActivatedRouteSnapshot, _handle: DetachedRouteHandle): void {}
  shouldAttach(_route: ActivatedRouteSnapshot): boolean { return false; }
  retrieve(_route: ActivatedRouteSnapshot): DetachedRouteHandle | null { return null; }

  shouldReuseRoute(future: ActivatedRouteSnapshot, curr: ActivatedRouteSnapshot): boolean {
    return future.routeConfig === curr.routeConfig;
  }
}
