import { Injectable } from '@angular/core';
import { Observable, of } from 'rxjs';
import { catchError, map, tap } from 'rxjs/operators';
import { TranslateService } from '@ngx-translate/core';
import { HttpErrorResponse } from '@angular/common/http';

import { ApiService, ResponseTypes, RequestOptions } from './api.service';

export enum AuthStates {
  AuthDisabled, Logged, NotLogged
}

/**
 * Allows to work with the user authentication. It uses the "admin" user account.
 */
@Injectable({
  providedIn: 'root'
})
export class AuthService {
  constructor(
    private apiService: ApiService,
    private translateService: TranslateService,
  ) { }

  /**
   * Logs in the user.
   */
  login(password: string): Observable<any> {
    return this.apiService.post('login', { username: 'admin', password: password }, new RequestOptions({ ignoreAuth: true }))
      .pipe(
        tap(status => {
          if (status !== true) {
            throw new Error();
          }
        }),
      );
  }

  /**
   * Checks if the user is logged in.
   */
  checkLogin(): Observable<AuthStates> {
    return this.apiService.get('user', new RequestOptions({ responseType: ResponseTypes.Text, ignoreAuth: true }))
      .pipe(
        map(() => AuthStates.Logged),
        catchError(err => {
          // The auth options are disabled in the backend.
          if ((err as HttpErrorResponse).status === 504) {
            return of(AuthStates.AuthDisabled);
          }

          // The user is not logged.
          if ((err as HttpErrorResponse).status === 401) {
            return of(AuthStates.NotLogged);
          }
        })
      );
  }

  /**
   * Logs out the user.
   */
  logout(): Observable<any> {
    return this.apiService.post('logout', {})
      .pipe(
        tap(status => {
          if (status !== true) {
            throw new Error();
          }
        }),
      );
  }

  /**
   * Changes the password.
   */
  changePassword(oldPass: string, newPass: string): Observable<any> {
    return this.apiService.post('change-password',
      { old_password: oldPass, new_password: newPass },
      new RequestOptions({ responseType: ResponseTypes.Text, ignoreAuth: true }))
      .pipe(map(result => {
        if (typeof result === 'string' && result.trim() === 'true') {
          return true;
        } else {
          if (result === 'Please do not change the default password.') {
            throw new Error(this.translateService.instant('settings.password.errors.default-password'));
          }

          throw new Error(this.translateService.instant('common.operation-error'));
        }
      }), catchError(err => {
        if (err && (err as HttpErrorResponse).status === 400) {
          throw new Error(this.translateService.instant('settings.password.errors.invalid-password-format'));
        } else if (err && (err as HttpErrorResponse).status === 401) {
          throw new Error(this.translateService.instant('settings.password.errors.bad-old-password'));
        }

        throw new Error(this.translateService.instant('common.operation-error'));
      }));
  }

  /**
   * Set the initial password for accessing the system. It only works if threre is not password yet.
   */
  initialConfig(pass: string): Observable<any> {
    return this.apiService.post('create-account',
      { username: 'admin', password: pass },
      new RequestOptions({ responseType: ResponseTypes.Text, ignoreAuth: true }))
      .pipe(map(result => {
        if (typeof result === 'string' && result.trim() === 'true') {
          return true;
        } else {
          throw new Error(result);
        }
      }), catchError(err => {
        if (err && (err as HttpErrorResponse).status === 400) {
          throw new Error(this.translateService.instant('settings.password.errors.invalid-password-format'));
        } else if (err && (err as HttpErrorResponse).status === 403) {
          throw new Error(this.translateService.instant('settings.password.initial-config.error'));
        }

        throw new Error(this.translateService.instant('common.operation-error'));
      }));
  }
}
