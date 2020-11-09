import { Injectable } from '@angular/core';
import { Observable, of, throwError } from 'rxjs';
import { catchError, map, tap } from 'rxjs/operators';
import { TranslateService } from '@ngx-translate/core';
import { HttpErrorResponse } from '@angular/common/http';

import { ApiService, ResponseTypes, RequestOptions } from './api.service';
import { OperationError } from '../utils/operation-error';
import { processServiceError } from '../utils/errors';
import { AuthGuardService } from './auth-guard.service';

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
    private authGuardService: AuthGuardService,
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

          this.authGuardService.forceFail = false;
        }),
      );
  }

  /**
   * Checks if the user is logged in.
   */
  checkLogin(): Observable<AuthStates> {
    return this.apiService.get('user', new RequestOptions({ ignoreAuth: true }))
      .pipe(
        map(response => {
          if (response.username) {
            return AuthStates.Logged;
          } else {
            return AuthStates.AuthDisabled;
          }
        }),
        catchError((err: OperationError) => {
          err = processServiceError(err);

          // The user is not logged.
          if (err.originalError && (err.originalError as HttpErrorResponse).status === 401) {
            this.authGuardService.forceFail = true;

            return of(AuthStates.NotLogged);
          }

          return throwError(err);
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

          this.authGuardService.forceFail = true;
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
      }), catchError((err: OperationError) => {
        err = processServiceError(err);

        if (err.originalError && (err.originalError as HttpErrorResponse).status === 401) {
          err.translatableErrorMsg = 'settings.password.errors.bad-old-password';
        }

        return throwError(err);
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
      }), catchError((err: OperationError) => {
        err = processServiceError(err);

        if (err.originalError && (err.originalError as HttpErrorResponse).status === 500) {
          err.translatableErrorMsg = 'settings.password.initial-config.error';
        }

        return throwError(err);
      }));
  }
}
