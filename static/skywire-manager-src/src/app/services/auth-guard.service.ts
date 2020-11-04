import { Injectable } from '@angular/core';
import { ActivatedRouteSnapshot, CanActivate, Router, RouterStateSnapshot, CanActivateChild } from '@angular/router';
import { Observable, of } from 'rxjs';

 /**
 * Redirects the user to the login page if the forceFail property is set to true. The api
 * service is in chage of redirecting the unauthorized users to the login page in other cases.
 * It must be used in the canActivate and canActivateChild properties of the routing module.
 */
@Injectable({
  providedIn: 'root'
})
export class AuthGuardService implements CanActivate, CanActivateChild {
  private forceFailInternal = false;
  /**
   * If true, the user will be redirected to the login page.
   */
  set forceFail(val: boolean) {
    this.forceFailInternal = val;
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
    if (this.forceFailInternal) {
      // Redirect the user.
      this.router.navigate(['login'], { replaceUrl: true });

      return of(false);
    }

    return of(true);
  }
}
