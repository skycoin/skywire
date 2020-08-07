import { Injectable } from '@angular/core';
import { ActivatedRouteSnapshot, CanActivate, Router, RouterStateSnapshot, CanActivateChild } from '@angular/router';
import { Observable, of } from 'rxjs';
import { map, catchError } from 'rxjs/operators';
import { MatDialog } from '@angular/material/dialog';

import { AuthService, AuthStates } from './auth.service';

/**
 * Redirects unauthorized users to the login page during the first load and always redirects
 * authorized users from the login page to the node list. The api service is in chage of
 * redirecting the unauthorized users to the login page in other cases.
 */
@Injectable({
  providedIn: 'root'
})
export class AuthGuardService implements CanActivate, CanActivateChild {
  private authChecked = false;

  constructor(
    private authService: AuthService,
    private router: Router,
    private matDialog: MatDialog,
  ) { }

  canActivate(route: ActivatedRouteSnapshot, state: RouterStateSnapshot): Observable<boolean> {
    return this.checkIfCanActivate(route);
  }

  canActivateChild(childRoute: ActivatedRouteSnapshot, state: RouterStateSnapshot): Observable<boolean> {
    return this.checkIfCanActivate(childRoute);
  }

  private checkIfCanActivate(route: ActivatedRouteSnapshot): Observable<boolean> {
    if (this.authChecked && route.routeConfig.path !== 'login') {
      return of(true);
    }

    return this.authService.checkLogin().pipe(catchError(e => {
      return of(AuthStates.AuthDisabled);
    }), map((authState: AuthStates) => {
      this.authChecked = true;

      // If the user is trying to access "Login" page while he is already logged in or the
      // auth is disabled, redirect him to "Nodes" page
      if (route.routeConfig.path === 'login' && (authState === AuthStates.Logged || authState === AuthStates.AuthDisabled)) {
        this.router.navigate(['nodes'], { replaceUrl: true });

        return false;
      }

      // If the user is trying to access a protected part of the application while not logged in,
      // redirect him to "Login" page
      if (route.routeConfig.path !== 'login' && (authState !== AuthStates.Logged && authState !== AuthStates.AuthDisabled)) {
        this.router.navigate(['login'], { replaceUrl: true });
        this.matDialog.closeAll();

        return false;
      }

      return true;
    }));
  }
}
