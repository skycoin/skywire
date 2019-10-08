import {Injectable} from '@angular/core';
import {ApiService} from './api.service';
import { Observable, of } from 'rxjs';
import { catchError, map, tap } from 'rxjs/operators';
import { TranslateService } from '@ngx-translate/core';
import { HttpErrorResponse } from '@angular/common/http';

export enum AUTH_STATE {
  AUTH_DISABLED, LOGIN_OK, LOGIN_FAIL, CHANGE_PASSWORD
}

@Injectable({
  providedIn: 'root'
})
export class AuthService {
  constructor(
    private apiService: ApiService,
    private translateService: TranslateService,
  ) { }

  login(password: string) {
    return this.apiService.post('login', { username: 'admin', password: password }, { api2: true, type: 'json', ignoreAuth: true })
      .pipe(
        tap(status => {
          if (status !== true) {
            throw new Error();
          }
        }),
      );
  }

  checkLogin(deactivateAuthRedirects = false): Observable<AUTH_STATE> {
    return this.apiService.get('user', { responseType: 'text', api2: true, ignoreAuth: deactivateAuthRedirects })
      .pipe(
        map(() => AUTH_STATE.LOGIN_OK),
        catchError(err => {
          if ((err as HttpErrorResponse).status === 504) {
            return of(AUTH_STATE.AUTH_DISABLED);
          }

          if ((err as HttpErrorResponse).status === 401 || err.error.includes('Unauthorized')) {
            return of(AUTH_STATE.LOGIN_FAIL);
          }

          if (err.error.includes('change password')) {
            return of(AUTH_STATE.CHANGE_PASSWORD);
          }
        })
      );
  }

  logout() {
    return this.apiService.post('logout', {}, { api2: true, type: 'json' })
      .pipe(
        tap(status => {
          if (status !== true) {
            throw new Error();
          }
        }),
      );
  }

  authToken(): Observable<string> {
    return this.apiService.post('checkLogin', {}, {responseType: 'text'});
  }

  changePassword(oldPass: string, newPass: string): Observable<boolean> {
    return this.apiService.post('change-password',
      {old_password: oldPass, new_password: newPass},
      { responseType: 'text', type: 'json', api2: true })
      .pipe(map(result => {
        if (typeof result === 'string' && result.trim() === 'true') {
          return true;
        } else {
          if (result === 'Please do not change the default password.') {
            throw new Error(this.translateService.instant('settings.password.errors.default-password'));
          }

          throw new Error(this.translateService.instant('settings.password.errors.bad-old-password'));
        }
      }), catchError(err => {
        if ((err as HttpErrorResponse).status === 400) {
          throw new Error(this.translateService.instant('settings.password.errors.invalid-password-format'));
        }

        throw new Error(this.translateService.instant('settings.password.errors.bad-old-password'));
      }));
  }
}
