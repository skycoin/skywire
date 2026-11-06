import {
  TranslateService
} from "./chunk-6WNZTOXO.js";
import {
  ApiService,
  RequestOptions,
  ResponseTypes,
  Router,
  processServiceError
} from "./chunk-ZMZW6HXB.js";
import {
  Injectable,
  catchError,
  map,
  of,
  tap,
  throwError
} from "./chunk-ZNLGPAUZ.js";

// src/app/services/auth-guard.service.ts
var __decorate = function(decorators, target, key, desc) {
  var c = arguments.length, r = c < 3 ? target : desc === null ? desc = Object.getOwnPropertyDescriptor(target, key) : desc, d;
  if (typeof Reflect === "object" && typeof Reflect.decorate === "function") r = Reflect.decorate(decorators, target, key, desc);
  else for (var i = decorators.length - 1; i >= 0; i--) if (d = decorators[i]) r = (c < 3 ? d(r) : c > 3 ? d(target, key, r) : d(target, key)) || r;
  return c > 3 && r && Object.defineProperty(target, key, r), r;
};
var AuthGuardService = class AuthGuardService2 {
  /**
   * If true, the user will be redirected to the login page.
   */
  set forceFail(val) {
    this.forceFailInternal = val;
  }
  constructor(router) {
    this.router = router;
    this.forceFailInternal = false;
  }
  canActivate(route, state) {
    return this.checkIfCanActivate();
  }
  canActivateChild(childRoute, state) {
    return this.checkIfCanActivate();
  }
  checkIfCanActivate() {
    if (this.forceFailInternal) {
      this.router.navigate(["login"], { replaceUrl: true });
      return of(false);
    }
    return of(true);
  }
  static {
    this.ctorParameters = () => [
      { type: Router }
    ];
  }
};
AuthGuardService = __decorate([
  Injectable({
    providedIn: "root"
  })
], AuthGuardService);

// src/app/services/auth.service.ts
var __decorate2 = function(decorators, target, key, desc) {
  var c = arguments.length, r = c < 3 ? target : desc === null ? desc = Object.getOwnPropertyDescriptor(target, key) : desc, d;
  if (typeof Reflect === "object" && typeof Reflect.decorate === "function") r = Reflect.decorate(decorators, target, key, desc);
  else for (var i = decorators.length - 1; i >= 0; i--) if (d = decorators[i]) r = (c < 3 ? d(r) : c > 3 ? d(target, key, r) : d(target, key)) || r;
  return c > 3 && r && Object.defineProperty(target, key, r), r;
};
var AuthStates;
(function(AuthStates2) {
  AuthStates2[AuthStates2["AuthDisabled"] = 0] = "AuthDisabled";
  AuthStates2[AuthStates2["Logged"] = 1] = "Logged";
  AuthStates2[AuthStates2["NotLogged"] = 2] = "NotLogged";
})(AuthStates || (AuthStates = {}));
var AuthService = class AuthService2 {
  constructor(apiService, translateService, authGuardService) {
    this.apiService = apiService;
    this.translateService = translateService;
    this.authGuardService = authGuardService;
  }
  /**
   * Logs in the user.
   */
  login(password) {
    return this.apiService.post("login", { username: "admin", password }, new RequestOptions({ ignoreAuth: true })).pipe(tap((status) => {
      if (status !== true) {
        throw new Error();
      }
      this.authGuardService.forceFail = false;
    }));
  }
  /**
   * Checks if the user is logged in.
   */
  checkLogin() {
    return this.apiService.get("user", new RequestOptions({ ignoreAuth: true })).pipe(map((response) => {
      if (response.username) {
        return AuthStates.Logged;
      } else {
        return AuthStates.AuthDisabled;
      }
    }), catchError((err) => {
      err = processServiceError(err);
      if (err.originalError && err.originalError.status === 401) {
        this.authGuardService.forceFail = true;
        return of(AuthStates.NotLogged);
      }
      return throwError(err);
    }));
  }
  /**
   * Logs out the user.
   */
  logout() {
    return this.apiService.post("logout", {}).pipe(tap((status) => {
      if (status !== true) {
        throw new Error();
      }
      this.authGuardService.forceFail = true;
    }));
  }
  /**
   * Changes the password.
   */
  changePassword(oldPass, newPass) {
    return this.apiService.post("change-password", { old_password: oldPass, new_password: newPass }, new RequestOptions({ responseType: ResponseTypes.Text, ignoreAuth: true })).pipe(map((result) => {
      if (typeof result === "string" && result.trim() === "true") {
        return true;
      } else {
        if (result === "Please do not change the default password.") {
          throw new Error(this.translateService.instant("settings.password.errors.default-password"));
        }
        throw new Error(this.translateService.instant("common.operation-error"));
      }
    }), catchError((err) => {
      err = processServiceError(err);
      if (err.originalError && err.originalError.status === 401) {
        err.translatableErrorMsg = "settings.password.errors.bad-old-password";
      }
      return throwError(err);
    }));
  }
  /**
   * Set the initial password for accessing the system. It only works if threre is not password yet.
   */
  initialConfig(pass) {
    return this.apiService.post("create-account", { username: "admin", password: pass }, new RequestOptions({ responseType: ResponseTypes.Text, ignoreAuth: true })).pipe(map((result) => {
      if (typeof result === "string" && result.trim() === "true") {
        return true;
      } else {
        throw new Error(result);
      }
    }), catchError((err) => {
      err = processServiceError(err);
      if (err.originalError && err.originalError.status === 500) {
        err.translatableErrorMsg = "settings.password.initial-config.error";
      }
      return throwError(err);
    }));
  }
  /**
   * Checks if an account has been created (public endpoint, no auth).
   */
  userExists() {
    return this.apiService.get("user-exists", new RequestOptions({ ignoreAuth: true })).pipe(
      map((response) => response.exists === true),
      catchError(() => of(true))
      // on error, assume user exists (safe default)
    );
  }
  static {
    this.ctorParameters = () => [
      { type: ApiService },
      { type: TranslateService },
      { type: AuthGuardService }
    ];
  }
};
AuthService = __decorate2([
  Injectable({
    providedIn: "root"
  })
], AuthService);

export {
  AuthStates,
  AuthService
};
//# debugId=3302244d-a54e-546f-bdc9-075d5e8aaa2f
//# sourceMappingURL=chunk-4XQ3VDCY.js.map
