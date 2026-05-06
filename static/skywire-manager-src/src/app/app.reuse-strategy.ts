import { RouteReuseStrategy, ActivatedRouteSnapshot, DetachedRouteHandle } from '@angular/router';
import { Injectable } from '@angular/core';

/**
 * Default-destroy reuse strategy with a small cache for routes
 * the user explicitly wants kept alive across navigation.
 *
 * Today the only such route is the per-visor terminal — leaving an
 * SSH-style command running in the dmsgpty terminal and switching
 * tabs shouldn't tear the websocket down. Cache key is per-visor
 * (`<visor-pk>/terminal`) so different visors keep separate
 * terminal sessions; navigating back to a cached visor's terminal
 * re-attaches the same xterm.js + websocket without spawning a
 * fresh proc.
 */
@Injectable()
export class AppReuseStrategy implements RouteReuseStrategy {

  // Persistent map keyed by `<visor-pk>/terminal`. Lives for the
  // duration of the SPA session.
  private static terminalHandles = new Map<string, DetachedRouteHandle>();

  /** Returns a cache key when `route` is a terminal route worth
   *  persisting, or empty string otherwise. */
  private terminalKey(route: ActivatedRouteSnapshot): string {
    if (!route || !route.routeConfig || route.routeConfig.path !== 'terminal') {
      return '';
    }
    // Walk up to find the visor PK segment (`:key` in the parent
    // node route).
    let cur: ActivatedRouteSnapshot | null = route.parent;
    while (cur) {
      const pk = cur.params['key'];
      if (pk) {
        return `${pk}/terminal`;
      }
      cur = cur.parent;
    }
    return '';
  }

  shouldDetach(route: ActivatedRouteSnapshot): boolean {
    return this.terminalKey(route) !== '';
  }

  store(route: ActivatedRouteSnapshot, handle: DetachedRouteHandle): void {
    const key = this.terminalKey(route);
    if (!key) { return; }
    if (handle) {
      AppReuseStrategy.terminalHandles.set(key, handle);
    } else {
      AppReuseStrategy.terminalHandles.delete(key);
    }
  }

  shouldAttach(route: ActivatedRouteSnapshot): boolean {
    const key = this.terminalKey(route);
    return key !== '' && AppReuseStrategy.terminalHandles.has(key);
  }

  retrieve(route: ActivatedRouteSnapshot): DetachedRouteHandle | null {
    const key = this.terminalKey(route);
    if (!key) { return null; }
    return AppReuseStrategy.terminalHandles.get(key) || null;
  }

  shouldReuseRoute(future: ActivatedRouteSnapshot, curr: ActivatedRouteSnapshot): boolean {
    // No same-route reuse — keep the original behavior. The
    // detach/attach machinery above handles the terminal case.
    return false;
  }
}
