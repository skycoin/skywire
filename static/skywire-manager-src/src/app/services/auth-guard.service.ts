import { Injectable } from '@angular/core';
import { ActivatedRouteSnapshot, CanActivate, Router, RouterStateSnapshot, CanActivateChild } from '@angular/router';
import { Observable } from 'rxjs';
import { map } from 'rxjs/operators';
import { MatDialog } from '@angular/material/dialog';

import { AuthService, AuthStates } from './auth.service';

/**
 * Makes sure of only allowing the user to access the system when having permission.
 */
@Injectable({
  providedIn: 'root'
})
export class AuthGuardService implements CanActivate, CanActivateChild {
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
    return this.authService.checkLogin().pipe(map((authState: AuthStates) => {
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
