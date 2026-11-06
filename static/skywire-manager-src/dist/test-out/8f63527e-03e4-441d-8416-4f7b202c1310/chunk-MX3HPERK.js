import {
  ApiService
} from "./chunk-ZMZW6HXB.js";
import {
  formatDate
} from "./chunk-VGD7AO6Y.js";
import {
  Injectable,
  map
} from "./chunk-ZNLGPAUZ.js";

// src/app/services/apps.service.ts
var __decorate = function(decorators, target, key, desc) {
  var c = arguments.length, r = c < 3 ? target : desc === null ? desc = Object.getOwnPropertyDescriptor(target, key) : desc, d;
  if (typeof Reflect === "object" && typeof Reflect.decorate === "function") r = Reflect.decorate(decorators, target, key, desc);
  else for (var i = decorators.length - 1; i >= 0; i--) if (d = decorators[i]) r = (c < 3 ? d(r) : c > 3 ? d(target, key, r) : d(target, key)) || r;
  return c > 3 && r && Object.defineProperty(target, key, r), r;
};
var AppsService = class AppsService2 {
  constructor(apiService) {
    this.apiService = apiService;
  }
  /**
   * Starts or stops an app.
   */
  changeAppState(nodeKey, appName, startApp) {
    return this.apiService.put(`visors/${nodeKey}/apps/${encodeURIComponent(appName)}`, { status: startApp ? 1 : 0 });
  }
  /**
   * Adds a new app entry to the visor's launcher config (used to
   * create additional instances of multi-instance apps like
   * skysocks-client-2). Returns the freshly-added AppState.
   */
  addApp(nodeKey, appName, binaryName) {
    return this.apiService.post(`visors/${nodeKey}/apps`, {
      name: appName,
      binary: binaryName
    });
  }
  /**
   * Sets / replaces / deletes env-var entries on an existing app.
   * Empty value means delete that key. Pairs with custom_setting to
   * configure multi-instance daemons (FIBER_TOML lives in env, the
   * rest live in args).
   */
  setAppEnv(nodeKey, appName, env) {
    return this.apiService.put(`visors/${nodeKey}/apps/${encodeURIComponent(appName)}`, { env });
  }
  /**
   * Universal-panel save: replaces the full args list (parsed
   * server-side from a shell-like string), the full env list, and
   * the persisted launcher mode in one round-trip. Any field can
   * be omitted to leave it untouched. The visor restarts the app
   * if any are present (same gate as custom_setting).
   */
  setAppFullConfig(nodeKey, appName, body) {
    return this.apiService.put(`visors/${nodeKey}/apps/${encodeURIComponent(appName)}`, body);
  }
  /**
   * Returns the cached `<binary> --help` output for an app. Backs
   * the universal-panel "Show flags" disclosure.
   */
  getAppHelp(nodeKey, appName) {
    return this.apiService.get(`visors/${nodeKey}/apps/${encodeURIComponent(appName)}/help`);
  }
  /**
   * Returns the flags an app accepts with their defaults and the values its
   * args set. Backs the generated flags form in the settings panel.
   */
  getAppFlags(nodeKey, appName) {
    return this.apiService.get(`visors/${nodeKey}/apps/${encodeURIComponent(appName)}/flags`);
  }
  /**
   * Changes the autostart setting of an app.
   */
  changeAppAutostart(nodeKey, appName, autostart) {
    return this.changeAppSettings(nodeKey, appName, { autostart });
  }
  /**
   * Changes the autostart setting of an app.
   */
  changeAppSettings(nodeKey, appName, settings) {
    return this.apiService.put(`visors/${nodeKey}/apps/${encodeURIComponent(appName)}`, settings);
  }
  /**
   * Get the log messages of an app.
   * @param days Number of days to take into account for logs. The result will contain log until the current date,
   * starting from "currentDate - days". If you want to get the entire log history, use -1.
   */
  getLogMessages(nodeKey, appName, days) {
    const since = days !== -1 ? Date.now() - days * 864e5 : 0;
    const sinceString = formatDate(since, "yyyy-MM-ddTHH:mm:ssZZZZZ", "en-US");
    return this.apiService.get(this.getLogMessagesUrl(nodeKey, appName) + `?since=${sinceString}`).pipe(map((response) => response.logs));
  }
  /**
   * Gets the partial URL for getting the logs of an app.
   */
  getLogMessagesUrl(nodeKey, appName) {
    return `visors/${nodeKey}/apps/${encodeURIComponent(appName)}/logs`;
  }
  static {
    this.ctorParameters = () => [
      { type: ApiService }
    ];
  }
};
AppsService = __decorate([
  Injectable({
    providedIn: "root"
  })
], AppsService);

export {
  AppsService
};
//# debugId=6a4ba139-a7af-52e6-8155-241b5bf582a7
//# sourceMappingURL=chunk-MX3HPERK.js.map
