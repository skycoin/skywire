import { Injectable } from '@angular/core';
import { ActivatedRouteSnapshot, CanActivate, Router, RouterStateSnapshot, CanActivateChild } from '@angular/router';
import { Observable, of } from 'rxjs';

 /**
 * Redirects the user to the error page of the VPN client if the lastError property is set. It
 * must be used in the canActivate and canActivateChild properties of the routing module.
 */
@Injectable({
  providedIn: 'root'
})
export class VpnAuthGuardService implements CanActivate, CanActivateChild {
  private lastErrorInternal: string;
  /**
   * When set, the user will be redirected to the error page of the VPN client, with
   * the provided error.
   */
  set lastError(val: string) {
    this.lastErrorInternal = val;
  }

  constructor(
    private router: Router,
  ) { }

  canActivate(route: ActivatedRouteSnapshot, state: RouterStateSnapshot): Observable<boolean> {
    return this.checkIfCanActivate();
  }

  canActivateChild(childRoute: ActivatedRouteSnapshot, state: RouterStateSnapshot): Observable<boolean> {
    return this.checkIfCanActivate();
  }

  private checkIfCanActivate(): Observable<boolean> {
    if (this.lastErrorInternal) {
      // Redirect the user.
      this.router.navigate(['vpn', 'unavailable'], { queryParams: {problem: this.lastErrorInternal} });

      return of(false);
    }

    return of(true);
  }
}
