import { of, throwError } from 'rxjs';
import { HttpErrorResponse } from '@angular/common/http';

import { AuthService, AuthStates } from './auth.service';
import { OperationError } from '../utils/operation-error';

// AuthService is a plain class whose only dependencies are ApiService,
// TranslateService and AuthGuardService — all mockable with spies — so we
// instantiate it directly rather than through TestBed. The tests pin the
// branch behaviour of each method: success side effects on AuthGuardService,
// the AuthStates mapping of checkLogin, and the error translation paths.
describe('AuthService', () => {
  let apiService: jasmine.SpyObj<{ get: any; post: any }>;
  let translateService: { instant: (key: string) => string };
  let authGuardService: { forceFail: boolean };
  let service: AuthService;

  beforeEach(() => {
    apiService = jasmine.createSpyObj('ApiService', ['get', 'post']);
    translateService = { instant: (key: string) => key };
    authGuardService = { forceFail: false };
    service = new AuthService(apiService as any, translateService as any, authGuardService as any);
  });

  describe('login', () => {
    it('clears forceFail and posts admin credentials on success', (done) => {
      authGuardService.forceFail = true;
      apiService.post.and.returnValue(of(true));

      service.login('secret').subscribe(() => {
        expect(authGuardService.forceFail).toBeFalse();
        expect(apiService.post).toHaveBeenCalledWith(
          'login', { username: 'admin', password: 'secret' }, jasmine.any(Object));
        done();
      });
    });

    it('errors and leaves forceFail set when the server does not return true', (done) => {
      authGuardService.forceFail = true;
      apiService.post.and.returnValue(of(false));

      service.login('secret').subscribe({
        next: () => fail('expected an error'),
        error: () => {
          expect(authGuardService.forceFail).toBeTrue();
          done();
        },
      });
    });
  });

  describe('checkLogin', () => {
    it('maps a response with a username to Logged', (done) => {
      apiService.get.and.returnValue(of({ username: 'admin' }));
      service.checkLogin().subscribe((state) => {
        expect(state).toBe(AuthStates.Logged);
        done();
      });
    });

    it('maps a response without a username to AuthDisabled', (done) => {
      apiService.get.and.returnValue(of({}));
      service.checkLogin().subscribe((state) => {
        expect(state).toBe(AuthStates.AuthDisabled);
        done();
      });
    });

    it('maps a 401 to NotLogged and sets forceFail', (done) => {
      apiService.get.and.returnValue(throwError(() => new HttpErrorResponse({ status: 401 })));
      service.checkLogin().subscribe((state) => {
        expect(state).toBe(AuthStates.NotLogged);
        expect(authGuardService.forceFail).toBeTrue();
        done();
      });
    });

    it('rethrows non-401 errors', (done) => {
      apiService.get.and.returnValue(throwError(() => new HttpErrorResponse({ status: 500 })));
      service.checkLogin().subscribe({
        next: () => fail('expected an error'),
        error: (err: OperationError) => {
          expect(err.originalError.status).toBe(500);
          expect(authGuardService.forceFail).toBeFalse();
          done();
        },
      });
    });
  });

  describe('logout', () => {
    it('sets forceFail on success', (done) => {
      apiService.post.and.returnValue(of(true));
      service.logout().subscribe(() => {
        expect(authGuardService.forceFail).toBeTrue();
        done();
      });
    });

    it('errors when the server does not return true', (done) => {
      apiService.post.and.returnValue(of(false));
      service.logout().subscribe({
        next: () => fail('expected an error'),
        error: (err) => {
          expect(err).toEqual(jasmine.any(Error));
          done();
        },
      });
    });
  });

  describe('changePassword', () => {
    it('resolves to true when the server returns the string "true"', (done) => {
      apiService.post.and.returnValue(of('true'));
      service.changePassword('old', 'new').subscribe((result) => {
        expect(result).toBeTrue();
        done();
      });
    });

    it('translates a 401 to the bad-old-password message', (done) => {
      apiService.post.and.returnValue(throwError(() => new HttpErrorResponse({ status: 401 })));
      service.changePassword('old', 'new').subscribe({
        next: () => fail('expected an error'),
        error: (err: OperationError) => {
          expect(err.translatableErrorMsg).toBe('settings.password.errors.bad-old-password');
          done();
        },
      });
    });
  });

  describe('initialConfig', () => {
    it('resolves to true when the server returns the string "true"', (done) => {
      apiService.post.and.returnValue(of('true'));
      service.initialConfig('pw').subscribe((result) => {
        expect(result).toBeTrue();
        done();
      });
    });

    it('translates a 500 to the initial-config error message', (done) => {
      apiService.post.and.returnValue(throwError(() => new HttpErrorResponse({ status: 500 })));
      service.initialConfig('pw').subscribe({
        next: () => fail('expected an error'),
        error: (err: OperationError) => {
          expect(err.translatableErrorMsg).toBe('settings.password.initial-config.error');
          done();
        },
      });
    });
  });

  describe('userExists', () => {
    it('is true when the server reports exists:true', (done) => {
      apiService.get.and.returnValue(of({ exists: true }));
      service.userExists().subscribe((exists) => {
        expect(exists).toBeTrue();
        done();
      });
    });

    it('is false when the server reports exists:false', (done) => {
      apiService.get.and.returnValue(of({ exists: false }));
      service.userExists().subscribe((exists) => {
        expect(exists).toBeFalse();
        done();
      });
    });

    it('defaults to true (assume account exists) on error', (done) => {
      apiService.get.and.returnValue(throwError(() => new HttpErrorResponse({ status: 500 })));
      service.userExists().subscribe((exists) => {
        expect(exists).toBeTrue();
        done();
      });
    });
  });
});
