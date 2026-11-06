import {
  countriesList
} from "./chunk-ED5WCGEB.js";
import {
  AppComponent
} from "./chunk-CIWHQAPM.js";
import "./chunk-KWVFHEEW.js";
import "./chunk-HLYS7KBE.js";
import "./chunk-B4GMTCLR.js";
import {
  AppsService
} from "./chunk-MX3HPERK.js";
import {
  AppConfig
} from "./chunk-CAA347WM.js";
import {
  TranslateService
} from "./chunk-6WNZTOXO.js";
import "./chunk-GPHL7TNH.js";
import "./chunk-H3STL4JE.js";
import "./chunk-XBRWDJ2O.js";
import {
  ApiService,
  HttpClient,
  RequestOptions,
  Router,
  SnackbarService,
  environment,
  processServiceError
} from "./chunk-ZMZW6HXB.js";
import "./chunk-CJYNOHF2.js";
import "./chunk-VGD7AO6Y.js";
import {
  StorageService
} from "./chunk-XOUXCPTJ.js";
import "./chunk-VNMLTXOX.js";
import {
  BehaviorSubject,
  Injectable,
  ReplaySubject,
  catchError,
  concat,
  delay,
  map,
  mergeMap,
  of,
  retryWhen,
  take,
  throwError
} from "./chunk-ZNLGPAUZ.js";
import "./chunk-ARXMZVRT.js";

// src/app/services/vpn-saved-data.service.ts
var __decorate = function(decorators, target, key, desc) {
  var c = arguments.length, r = c < 3 ? target : desc === null ? desc = Object.getOwnPropertyDescriptor(target, key) : desc, d;
  if (typeof Reflect === "object" && typeof Reflect.decorate === "function") r = Reflect.decorate(decorators, target, key, desc);
  else for (var i = decorators.length - 1; i >= 0; i--) if (d = decorators[i]) r = (c < 3 ? d(r) : c > 3 ? d(target, key, r) : d(target, key)) || r;
  return c > 3 && r && Object.defineProperty(target, key, r), r;
};
var ServerFlags;
(function(ServerFlags2) {
  ServerFlags2["None"] = "None";
  ServerFlags2["Favorite"] = "Favorite";
  ServerFlags2["Blocked"] = "Blocked";
})(ServerFlags || (ServerFlags = {}));
var DataUnits;
(function(DataUnits2) {
  DataUnits2["BitsSpeedAndBytesVolume"] = "BitsSpeedAndBytesVolume";
  DataUnits2["OnlyBytes"] = "OnlyBytes";
  DataUnits2["OnlyBits"] = "OnlyBits";
})(DataUnits || (DataUnits = {}));
var VpnSavedDataService = class VpnSavedDataService2 {
  constructor(router, storageService) {
    this.router = router;
    this.storageService = storageService;
    this.maxHistoryElements = 30;
    this.savedServersStorageKey = "VpnServers";
    this.checkIpSettingStorageKey = "VpnGetIp";
    this.dataUnitsSettingStorageKey = "VpnDataUnits";
    this.serversMap = /* @__PURE__ */ new Map();
    this.savedDataVersion = 0;
    this.currentServerSubject = new ReplaySubject(1);
    this.historySubject = new ReplaySubject(1);
    this.favoritesSubject = new ReplaySubject(1);
    this.blockedSubject = new ReplaySubject(1);
  }
  /**
   * Loads the data from local storage and updates all the vars, so the service can work correctly.
   */
  initialize() {
    this.migrateDataToHvStorage();
    this.serversMap = /* @__PURE__ */ new Map();
    const retrievedServers = this.storageService.getDataForHv(this.savedServersStorageKey);
    if (retrievedServers) {
      const servers = JSON.parse(retrievedServers);
      servers.serverList.forEach((server) => {
        this.serversMap.set(server.pk, server);
      });
      this.savedDataVersion = servers.version;
      if (servers.selectedServerPk) {
        this.updateCurrentServerPk(servers.selectedServerPk);
      }
    }
    this.launchListEvents();
  }
  /**
   * It checks if there is data saved from a previous version of the app, without being assigned to a
   * hypervisor. If the functions finds old data, it is migrated to the new format and assigned to the
   * current hypervisor.
   */
  migrateDataToHvStorage() {
    const oldSavedServers = localStorage.getItem(this.savedServersStorageKey);
    if (oldSavedServers) {
      this.storageService.setDataForHv(this.savedServersStorageKey, oldSavedServers);
      localStorage.removeItem(this.savedServersStorageKey);
    }
    const oldSavedIpSetting = localStorage.getItem(this.checkIpSettingStorageKey);
    if (oldSavedIpSetting) {
      this.storageService.setDataForHv(this.checkIpSettingStorageKey, oldSavedIpSetting);
      localStorage.removeItem(this.checkIpSettingStorageKey);
    }
    const oldSavedDataUnitsSetting = localStorage.getItem(this.dataUnitsSettingStorageKey);
    if (oldSavedDataUnitsSetting) {
      this.storageService.setDataForHv(this.dataUnitsSettingStorageKey, oldSavedDataUnitsSetting);
      localStorage.removeItem(this.dataUnitsSettingStorageKey);
    }
  }
  /**
   * Currently selected server.
   */
  get currentServer() {
    return this.currentServerPk ? this.serversMap.get(this.currentServerPk) : void 0;
  }
  /**
   * Observable which emits the currently selected server after each change.
   */
  get currentServerObservable() {
    return this.currentServerSubject.asObservable();
  }
  /**
   * Used servers history. If any of the returned values is changed, the updateServer function
   * must be called after that, with the modified server.
   */
  get history() {
    return this.historySubject.asObservable();
  }
  /**
   * Favorite servers list. If any of the returned values is changed, the updateServer function
   * must be called after that, with the modified server.
   */
  get favorites() {
    return this.favoritesSubject.asObservable();
  }
  /**
   * Blocked servers list. If any of the returned values is changed, the updateServer function
   * must be called after that, with the modified server.
   */
  get blocked() {
    return this.blockedSubject.asObservable();
  }
  /**
   * Gets the locally saved data of a server. If the server has not been saved, returns undefined.
   * @param pk Public key of the server.
   * @param updateFromPersistentStorage If true, the data will be updated from local storage
   * before returning the result. This ensures the most recent data is returned, in case it
   * was changed in another instance of the app, but this is not recommended for usage inside
   * loops, in which case the data should be update before making multiple calls to this function.
   */
  getSavedVersion(pk, updateFromPersistentStorage) {
    if (updateFromPersistentStorage) {
      this.checkIfDataWasChanged();
    }
    return this.serversMap.get(pk);
  }
  /**
   * Returns if the app should check the current local IP (true) or not (false).
   * If the user has not changed the setting, it returns true by default.
   */
  getCheckIpSetting() {
    const val = this.storageService.getDataForHv(this.checkIpSettingStorageKey);
    if (val === null || val === void 0) {
      return true;
    }
    return val !== "false";
  }
  /**
   * Sets if the app should check the current local IP (true) or not (false).
   */
  setCheckIpSetting(value) {
    this.storageService.setDataForHv(this.checkIpSettingStorageKey, value ? "true" : "false");
  }
  /**
   * Returs the data units that must be shown in the UI. If the user has not changed
   * the setting, it returns DataUnits.BitsSpeedAndBytesVolume by default.
   */
  getDataUnitsSetting() {
    const val = this.storageService.getDataForHv(this.dataUnitsSettingStorageKey);
    if (val === null || val === void 0) {
      return DataUnits.BitsSpeedAndBytesVolume;
    }
    return val;
  }
  /**
   * Sets the data units that must be shown in the UI.
   */
  setDataUnitsSetting(value) {
    this.storageService.setDataForHv(this.dataUnitsSettingStorageKey, value);
  }
  /**
   * Updates the data of the locally saved servers with the data obtained from the
   * discovery service.
   * @param serverList Servers obtained from the discovery service.
   */
  updateFromDiscovery(serverList) {
    this.checkIfDataWasChanged();
    serverList.forEach((server) => {
      if (this.serversMap.has(server.pk)) {
        const savedServer = this.serversMap.get(server.pk);
        savedServer.countryCode = server.countryCode;
        savedServer.name = server.name;
        savedServer.location = server.location;
        savedServer.note = server.note;
      }
    });
    this.saveData();
  }
  /**
   * Updates the data of a server and saves it.
   */
  updateServer(server) {
    this.serversMap.set(server.pk, server);
    this.cleanServers();
    this.saveData();
  }
  /**
   * Creates a LocalServerData instance from a VpnServer instance. If the server has already
   * been saved, the saved version is updated with the provided data and returned.
   */
  processFromDiscovery(newServer) {
    this.checkIfDataWasChanged();
    const retrievedServer = this.serversMap.get(newServer.pk);
    if (retrievedServer) {
      retrievedServer.countryCode = newServer.countryCode;
      retrievedServer.name = newServer.name;
      retrievedServer.location = newServer.location;
      retrievedServer.note = newServer.note;
      this.saveData();
      return retrievedServer;
    }
    return {
      countryCode: newServer.countryCode,
      name: newServer.name,
      customName: null,
      pk: newServer.pk,
      lastUsed: 0,
      inHistory: false,
      flag: ServerFlags.None,
      location: newServer.location,
      personalNote: null,
      note: newServer.note,
      enteredManually: false
    };
  }
  /**
   * Creates a LocalServerData instance from a ManualVpnServerData instance. If server the has already
   * been saved, the saved version is updated with the provided data and returned.
   */
  processFromManual(newServer) {
    this.checkIfDataWasChanged();
    const retrievedServer = this.serversMap.get(newServer.pk);
    if (retrievedServer) {
      retrievedServer.customName = newServer.name ?? null;
      retrievedServer.personalNote = newServer.note ?? null;
      retrievedServer.enteredManually = true;
      this.saveData();
      return retrievedServer;
    }
    return {
      countryCode: "zz",
      name: "",
      customName: newServer.name ?? null,
      pk: newServer.pk,
      lastUsed: 0,
      inHistory: false,
      flag: ServerFlags.None,
      location: "",
      personalNote: newServer.note ?? null,
      note: "",
      enteredManually: true
    };
  }
  /**
   * Changes the flag property of a server and saves the changes.
   * If the flag is the same, nothing happens.
   */
  changeFlag(server, flag) {
    this.checkIfDataWasChanged();
    const retrievedServer = this.serversMap.get(server.pk);
    if (retrievedServer) {
      server = retrievedServer;
    }
    if (server.flag === flag) {
      return;
    }
    server.flag = flag;
    if (!this.serversMap.has(server.pk)) {
      this.serversMap.set(server.pk, server);
    }
    this.cleanServers();
    this.saveData();
  }
  /**
   * Removes a server from the history.
   * @param pk Public key of the server to remove.
   */
  removeFromHistory(pk) {
    this.checkIfDataWasChanged();
    const retrievedServer = this.serversMap.get(pk);
    if (!retrievedServer || !retrievedServer.inHistory) {
      return;
    }
    retrievedServer.inHistory = false;
    this.cleanServers();
    this.saveData();
  }
  /**
   * Changes the currently selected server.
   */
  modifyCurrentServer(newServer) {
    this.checkIfDataWasChanged();
    if (newServer.pk === this.currentServerPk) {
      return;
    }
    if (!this.serversMap.has(newServer.pk)) {
      this.serversMap.set(newServer.pk, newServer);
    }
    this.updateCurrentServerPk(newServer.pk);
    this.cleanServers();
    this.saveData();
  }
  /**
   * Checks if the provided public key is the same of the currently selected server. If it is not,
   * the currently selected server is changed to the one with the provided pk. If no server
   * with the provided pk is found, one is created.
   */
  compareCurrentServer(pk) {
    this.checkIfDataWasChanged();
    if (pk) {
      if (!this.currentServerPk || this.currentServerPk !== pk) {
        this.currentServerPk = pk;
        const retrievedServer = this.serversMap.get(pk);
        if (!retrievedServer) {
          const server = this.processFromManual({ pk });
          this.serversMap.set(server.pk, server);
          this.cleanServers();
        }
        this.saveData();
        this.currentServerSubject.next(this.currentServer);
      }
    } else {
      if (this.currentServerPk) {
        this.currentServerPk = null;
        this.saveData();
        this.currentServerSubject.next(this.currentServer);
      }
    }
  }
  /**
   * Updates the history to make it have the currently selected server as the most recently
   * used one.
   */
  updateHistory() {
    this.checkIfDataWasChanged();
    const current = this.currentServer;
    if (!current) {
      return;
    }
    current.lastUsed = Date.now();
    current.inHistory = true;
    let historyList = [];
    this.serversMap.forEach((server) => {
      if (server.inHistory) {
        historyList.push(server);
      }
    });
    historyList = historyList.sort((a, b) => b.lastUsed - a.lastUsed);
    let historyElementsFound = 0;
    historyList.forEach((server) => {
      if (historyElementsFound < this.maxHistoryElements) {
        historyElementsFound += 1;
      } else {
        server.inHistory = false;
      }
    });
    this.cleanServers();
    this.saveData();
  }
  /**
   * Removes from the saved server list all unneeded servers. Only the currently selected server
   * and the servers that are in a special server list or have any personal data attached
   * are needed. The serversMap property is modified, but the changes are not saved to
   * persistent storage.
   */
  cleanServers() {
    const unneeded = [];
    this.serversMap.forEach((server) => {
      if (!server.inHistory && server.flag === ServerFlags.None && server.pk !== this.currentServerPk && !server.customName && !server.personalNote) {
        unneeded.push(server.pk);
      }
    });
    unneeded.forEach((pk) => {
      this.serversMap.delete(pk);
    });
  }
  /**
   * Saves to persistent storage the servers in serversMap and the PK of the currently selected
   * server. After that, the server list events are updated. checkIfDataWasChanged() must be
   * called before calling this function, to ensure the data is updated to the latest version
   * saved to persitent storage, to avoid problems with other instances of the app that may be
   * running at the same time.
   */
  saveData() {
    let lastSavedVersion = 0;
    const retrievedServers = this.storageService.getDataForHv(this.savedServersStorageKey);
    if (retrievedServers) {
      const servers = JSON.parse(retrievedServers);
      lastSavedVersion = servers.version;
    }
    if (lastSavedVersion !== this.savedDataVersion) {
      this.router.navigate(["vpn", "unavailable"], { queryParams: { problem: "storage" } });
      return;
    }
    this.savedDataVersion += 1;
    const data = {
      version: this.savedDataVersion,
      serverList: Array.from(this.serversMap.values()),
      selectedServerPk: this.currentServerPk
    };
    const dataToSave = JSON.stringify(data);
    this.storageService.setDataForHv(this.savedServersStorageKey, dataToSave);
    this.launchListEvents();
  }
  /**
   * Check if the data saved to persistent storage is more recent than the one currently loaded
   * in the app. If that is the case, the server list and the PK of the selected server are
   * updated with the data obtained from persistent storage. This allows to avoid problems when
   * more than one instance of the app is running at the same time.
   */
  checkIfDataWasChanged() {
    let lastSavedVersion = 0;
    const retrievedServers = this.storageService.getDataForHv(this.savedServersStorageKey);
    if (retrievedServers) {
      const servers = JSON.parse(retrievedServers);
      lastSavedVersion = servers.version;
    }
    if (lastSavedVersion !== this.savedDataVersion) {
      this.initialize();
    }
  }
  /**
   * Launches the server list events, with the most recent values.
   */
  launchListEvents() {
    const history = [];
    const favorites = [];
    const blocked = [];
    this.serversMap.forEach((server) => {
      if (server.inHistory) {
        history.push(server);
      }
      if (server.flag === ServerFlags.Favorite) {
        favorites.push(server);
      }
      if (server.flag === ServerFlags.Blocked) {
        blocked.push(server);
      }
    });
    this.historySubject.next(history);
    this.favoritesSubject.next(favorites);
    this.blockedSubject.next(blocked);
  }
  /**
   * Updates the currentServerPk var and sends the event with the updated server.
   * Must be called only after the server is in the servers map.
   */
  updateCurrentServerPk(pk) {
    this.currentServerPk = pk;
    this.currentServerSubject.next(this.currentServer);
  }
  static {
    this.ctorParameters = () => [
      { type: Router },
      { type: StorageService }
    ];
  }
};
VpnSavedDataService = __decorate([
  Injectable({
    providedIn: "root"
  })
], VpnSavedDataService);

// src/app/services/vpn-client.service.ts
var __decorate2 = function(decorators, target, key, desc) {
  var c = arguments.length, r = c < 3 ? target : desc === null ? desc = Object.getOwnPropertyDescriptor(target, key) : desc, d;
  if (typeof Reflect === "object" && typeof Reflect.decorate === "function") r = Reflect.decorate(decorators, target, key, desc);
  else for (var i = decorators.length - 1; i >= 0; i--) if (d = decorators[i]) r = (c < 3 ? d(r) : c > 3 ? d(target, key, r) : d(target, key)) || r;
  return c > 3 && r && Object.defineProperty(target, key, r), r;
};
var AppState;
(function(AppState2) {
  AppState2["Stopped"] = "stopped";
  AppState2["Connecting"] = "Connecting";
  AppState2["Running"] = "Running";
  AppState2["ShuttingDown"] = "Shutting down";
  AppState2["Reconnecting"] = "Connection failed, reconnecting";
})(AppState || (AppState = {}));
var BackendState = class {
  constructor() {
    this.updateDate = Date.now();
  }
};
var VpnClientAppData = class {
};
var VpnClientConnectionsData = class {
  constructor() {
    this.latency = 0;
    this.uploadSpeed = 0;
    this.downloadSpeed = 0;
    this.totalUploaded = 0;
    this.totalDownloaded = 0;
    this.connectionDuration = 0;
    this.error = "";
  }
};
var VpnServiceStates;
(function(VpnServiceStates2) {
  VpnServiceStates2[VpnServiceStates2["PerformingInitialCheck"] = 1] = "PerformingInitialCheck";
  VpnServiceStates2[VpnServiceStates2["Off"] = 10] = "Off";
  VpnServiceStates2[VpnServiceStates2["Starting"] = 20] = "Starting";
  VpnServiceStates2[VpnServiceStates2["Running"] = 100] = "Running";
  VpnServiceStates2[VpnServiceStates2["Disconnecting"] = 200] = "Disconnecting";
})(VpnServiceStates || (VpnServiceStates = {}));
var CheckPkResults;
(function(CheckPkResults2) {
  CheckPkResults2[CheckPkResults2["Busy"] = 1] = "Busy";
  CheckPkResults2[CheckPkResults2["Ok"] = 2] = "Ok";
  CheckPkResults2[CheckPkResults2["MustStop"] = 3] = "MustStop";
  CheckPkResults2[CheckPkResults2["SamePkRunning"] = 4] = "SamePkRunning";
  CheckPkResults2[CheckPkResults2["SamePkStopped"] = 5] = "SamePkStopped";
})(CheckPkResults || (CheckPkResults = {}));
var VpnClientService = class VpnClientService2 {
  constructor(apiService, appsService, router, vpnSavedDataService, http, snackbarService, translateService) {
    this.apiService = apiService;
    this.appsService = appsService;
    this.router = router;
    this.vpnSavedDataService = vpnSavedDataService;
    this.http = http;
    this.snackbarService = snackbarService;
    this.translateService = translateService;
    this.vpnClientAppName = "vpn-client";
    this.standardWaitTime = 2e3;
    this.stateSubject = new BehaviorSubject(null);
    this.errorSubject = new BehaviorSubject(false);
    this.working = true;
    this.requestedServer = null;
    this.requestedMuxRoutes = 0;
    this.requestedMinHops = 0;
    this.updatesStopped = false;
    this.currentEventData = new BackendState();
    this.currentEventData.busy = true;
    this.lastServiceState = VpnServiceStates.PerformingInitialCheck;
  }
  /**
   * Makes the initializations for the service to work. Can be called multiple times, provided
   * that the local visor PK is not changed.
   * @param nodeKey Local visor public key.
   */
  initialize(nodeKey) {
    if (nodeKey) {
      if (!this.nodeKey) {
        this.nodeKey = nodeKey;
        this.vpnSavedDataService.initialize();
        this.updateData();
      } else {
        if (nodeKey !== this.nodeKey) {
          this.router.navigate(["vpn", "unavailable"], { queryParams: { problem: "pkChange" } });
        } else if (this.updatesStopped) {
          this.updatesStopped = false;
          this.updateData();
        }
      }
    }
  }
  /**
   * Observable which continually emits state updates.
   */
  get backendState() {
    return this.stateSubject.asObservable();
  }
  /**
   * Observable which continually emits if there are errors connecting to the backend.
   * It only informs if an error was found during the last status request.
   */
  get errorsConnecting() {
    return this.errorSubject.asObservable();
  }
  /**
   * Makes the service update the VPN state immediately and continue doing so periodically.
   */
  updateData() {
    this.continuallyUpdateData(0);
  }
  /**
   * Starts the VPN.
   * @returns If it was possible to start the process (true) or not (false).
   */
  start() {
    if (!this.working && this.lastServiceState < 20) {
      this.changeAppState(true);
      return true;
    }
    return false;
  }
  /**
   * Stops the VPN.
   * @returns If it was possible to start the process (true) or not (false).
   */
  stop() {
    if (!this.working && this.lastServiceState >= 20 && this.lastServiceState < 200) {
      this.changeAppState(false);
      return true;
    }
    return false;
  }
  /**
   * Gets the public IP of the machine running this app. If there is an error, it could
   * return null.
   */
  getIpData() {
    if (!environment.production && AppConfig.vpn.hardcodedIpWhileDeveloping) {
      return of(["8.8.8.8 (testing)", "United States (testing)"]);
    }
    return this.http.request("GET", window.location.protocol + "//ip.skycoin.com/").pipe(retryWhen((errors) => concat(errors.pipe(delay(this.standardWaitTime), take(4)), throwError(""))), map((data) => {
      const ip = data && data["ip_address"] ? data["ip_address"] : this.translateService.instant("common.unknown");
      const country = data && data["country_name"] ? data["country_name"] : this.translateService.instant("common.unknown");
      return [ip, country];
    }));
  }
  /**
   * Connects to an already-selected server using the provided route options.
   * This is the action behind the Start button on the status page: selecting
   * a server in the list only marks it as current (no connection); this
   * method applies the server PK + route options and starts the VPN.
   * @param server The server to connect to (the current selected server).
   * @param muxRoutes Parallel multiplexed routes (>1 to enable). 0/1 = plain.
   * @param minHops Minimum hops (>=2 to force multihop). 0/1 = direct allowed.
   * @returns If it was possible to start the process (true) or not (false).
   */
  connectToServer(server, muxRoutes, minHops) {
    this.requestedServer = server;
    this.requestedMuxRoutes = muxRoutes && muxRoutes > 0 ? muxRoutes : 0;
    this.requestedMinHops = minHops && minHops > 0 ? minHops : 0;
    return this.changeServer();
  }
  /**
   * Changes the currently selected server and connects to it.
   * @returns If it was possible to start the process (true) or not (false).
   */
  changeServerUsingHistory(newServer) {
    this.requestedServer = newServer;
    return this.changeServer();
  }
  /**
   * Changes the currently selected server and connects to it.
   * @returns If it was possible to start the process (true) or not (false).
   */
  changeServerUsingDiscovery(newServer) {
    this.requestedServer = this.vpnSavedDataService.processFromDiscovery(newServer);
    return this.changeServer();
  }
  /**
   * Changes the currently selected server and connects to it.
   * @returns If it was possible to start the process (true) or not (false).
   */
  changeServerManually(newServer) {
    this.requestedServer = this.vpnSavedDataService.processFromManual(newServer);
    return this.changeServer();
  }
  /**
   * Starts the process for changing the selected server to the one in the requestedServer var.
   * @returns If it was possible to start the process (true) or not (false).
   */
  changeServer() {
    if (!this.working) {
      if (!this.stop()) {
        this.processServerChange();
      }
      return true;
    }
    return false;
  }
  /**
   * Checks if at this moment it is possible to change the selected server to the provided PK and
   * what must be done for the change to work.
   */
  checkNewPk(newPk) {
    if (this.working) {
      return CheckPkResults.Busy;
    } else if (this.lastServiceState !== VpnServiceStates.Off) {
      if (newPk === this.vpnSavedDataService.currentServer?.pk) {
        return CheckPkResults.SamePkRunning;
      } else {
        return CheckPkResults.MustStop;
      }
    } else if (this.vpnSavedDataService.currentServer && newPk === this.vpnSavedDataService.currentServer.pk) {
      return CheckPkResults.SamePkStopped;
    }
    return CheckPkResults.Ok;
  }
  /**
   * Changes the currently selected server to the one set in this.requestedServer. After that,
   * the VPN is started.
   */
  processServerChange() {
    if (!this.nodeKey) {
      return;
    }
    if (this.dataSubscription) {
      this.dataSubscription.unsubscribe();
    }
    const data = { pk: this.requestedServer.pk };
    data.env = {
      VPNMUXROUTES: String(this.requestedMuxRoutes > 1 ? this.requestedMuxRoutes : 0),
      VPNMINHOPS: String(this.requestedMinHops >= 2 ? this.requestedMinHops : 0)
    };
    this.stopContinuallyUpdatingData();
    this.working = true;
    this.sendUpdate();
    this.dataSubscription = this.appsService.changeAppSettings(this.nodeKey, this.vpnClientAppName, data).subscribe(() => {
      this.vpnSavedDataService.modifyCurrentServer(this.requestedServer);
      this.requestedServer = null;
      this.requestedMuxRoutes = 0;
      this.requestedMinHops = 0;
      this.working = false;
      this.start();
    }, (err) => {
      err = processServiceError(err);
      this.snackbarService.showError("vpn.server-change.backend-error", null, false, err.originalServerErrorMsg);
      this.working = false;
      this.requestedServer = null;
      this.requestedMuxRoutes = 0;
      this.requestedMinHops = 0;
      this.sendUpdate();
      this.updateData();
    });
  }
  /**
   * Starts or stops the VPN client app in the local visor, which starts or stops the VPN
   * protection.
   * @param startApp If the app must be started or stopped.
   */
  changeAppState(startApp) {
    if (this.working || !this.nodeKey) {
      return;
    }
    this.stopContinuallyUpdatingData();
    this.working = true;
    this.sendUpdate();
    const data = { status: 1 };
    if (startApp) {
      this.lastServiceState = VpnServiceStates.Starting;
      this.connectionHistoryPk = null;
    } else {
      this.lastServiceState = VpnServiceStates.Disconnecting;
      data.status = 0;
    }
    if (this.dataSubscription) {
      this.dataSubscription.unsubscribe();
    }
    this.dataSubscription = this.appsService.changeAppSettings(this.nodeKey, this.vpnClientAppName, data).pipe(
      /* eslint-disable arrow-body-style */
      catchError((err) => {
        return this.getVpnClientState().pipe(mergeMap((appData) => {
          if (appData) {
            if (startApp && appData.running) {
              return of(true);
            } else if (!startApp && !appData.running) {
              return of(true);
            }
          }
          return throwError(err);
        }));
      }),
      retryWhen((errors) => concat(errors.pipe(delay(this.standardWaitTime), take(3)), errors.pipe(mergeMap((err) => throwError(err)))))
    ).subscribe((appData) => {
      this.working = false;
      const vpnClientData = this.processAppData(appData);
      if (vpnClientData.running) {
        this.lastServiceState = VpnServiceStates.Running;
      } else {
        this.lastServiceState = VpnServiceStates.Off;
      }
      this.currentEventData.vpnClientAppData = vpnClientData;
      this.currentEventData.updateDate = Date.now();
      this.sendUpdate();
      this.updateData();
      if (!startApp && this.requestedServer) {
        this.processServerChange();
      }
    }, (err) => {
      err = processServiceError(err);
      if (this.lastServiceState === VpnServiceStates.Starting) {
        this.snackbarService.showError("vpn.status-page.problem-starting-error", null, false, err.originalServerErrorMsg);
      } else if (this.lastServiceState === VpnServiceStates.Disconnecting) {
        this.snackbarService.showError("vpn.status-page.problem-stopping-error", null, false, err.originalServerErrorMsg);
      } else {
        this.snackbarService.showError("vpn.status-page.generic-problem-error", null, false, err.originalServerErrorMsg);
      }
      this.working = false;
      this.sendUpdate();
      this.updateData();
    });
  }
  /**
   * Starts continually getting the state of the VPN and sending updates.
   * @param delayMs Delay, in ms, before starting to get the data.
   */
  continuallyUpdateData(delayMs) {
    if (this.working && this.lastServiceState !== VpnServiceStates.PerformingInitialCheck) {
      return;
    }
    if (this.continuousUpdateSubscription) {
      this.continuousUpdateSubscription.unsubscribe();
    }
    let retries = 0;
    this.continuousUpdateSubscription = of(0).pipe(delay(delayMs), mergeMap(() => this.getVpnClientState()), retryWhen((err) => err.pipe(mergeMap((error) => {
      this.errorSubject.next(true);
      AppComponent.currentInstance.showDataProblemMsg();
      error = processServiceError(error);
      if (error.originalError && error.originalError.status && error.originalError.status === 401) {
        return throwError(error);
      }
      if (this.lastServiceState !== VpnServiceStates.PerformingInitialCheck || retries < 4) {
        retries += 1;
        return of(error).pipe(delay(this.standardWaitTime));
      } else {
        return throwError(error);
      }
    })))).subscribe((appData) => {
      if (appData) {
        this.errorSubject.next(false);
        AppComponent.currentInstance.hideDataProblemMsg();
        if (this.lastServiceState === VpnServiceStates.PerformingInitialCheck) {
          this.working = false;
        }
        if (appData.running || !this.vpnSavedDataService.currentServer) {
          this.vpnSavedDataService.compareCurrentServer(appData.serverPk);
        }
        if (appData.running) {
          this.lastServiceState = VpnServiceStates.Running;
        } else {
          this.lastServiceState = VpnServiceStates.Off;
        }
        this.currentEventData.vpnClientAppData = appData;
        this.currentEventData.updateDate = Date.now();
        this.sendUpdate();
      } else if (this.lastServiceState === VpnServiceStates.PerformingInitialCheck) {
        this.router.navigate(["vpn", "unavailable"]);
        this.nodeKey = null;
        this.updatesStopped = true;
      }
      this.continuallyUpdateData(this.standardWaitTime);
    }, (error) => {
      error = processServiceError(error);
      if (error.originalError && error.originalError.status && error.originalError.status === 401) {
      } else {
        this.router.navigate(["vpn", "unavailable"]);
        this.nodeKey = null;
      }
      this.updatesStopped = true;
    });
  }
  /**
   * Makes the service stop continually updating the VPN state.
   */
  stopContinuallyUpdatingData() {
    if (this.continuousUpdateSubscription) {
      this.continuousUpdateSubscription.unsubscribe();
    }
  }
  /**
   * Gets the current state of the VPN.
   */
  getVpnClientState() {
    let vpnClientData;
    const options = new RequestOptions();
    options.vpnKeyForAuth = this.nodeKey;
    return this.apiService.get(`visors/${this.nodeKey}/summary`, options).pipe(mergeMap((nodeInfo) => {
      let appData;
      if (nodeInfo && nodeInfo.overview) {
        if (nodeInfo.overview.public_ip && nodeInfo.overview.public_ip.trim()) {
          this.visorPublicIp = nodeInfo.overview.public_ip;
        } else {
          this.visorPublicIp = null;
        }
        if (nodeInfo.overview.country_code) {
          this.visorCountryCode = nodeInfo.overview.country_code;
          const countryName = countriesList[nodeInfo.overview.country_code.toUpperCase()];
          this.visorCountryName = countryName ? countryName : nodeInfo.overview.country_code;
        } else {
          this.visorCountryCode = null;
          this.visorCountryName = null;
        }
      }
      if (nodeInfo && nodeInfo.overview && nodeInfo.overview.apps && nodeInfo.overview.apps.length > 0) {
        nodeInfo.overview.apps.forEach((value) => {
          if (value.name === this.vpnClientAppName) {
            appData = value;
          }
        });
      }
      if (appData) {
        vpnClientData = this.processAppData(appData);
      }
      vpnClientData.minHops = nodeInfo.min_hops ? nodeInfo.min_hops : 0;
      if (vpnClientData && vpnClientData.running) {
        const o = new RequestOptions();
        o.vpnKeyForAuth = this.nodeKey;
        return this.apiService.get(`visors/${this.nodeKey}/apps/${this.vpnClientAppName}/connections`, o);
      }
      return of(null);
    }), map((connectionsInfo) => {
      if (connectionsInfo && connectionsInfo.length > 0) {
        const vpnClientConnectionsData = new VpnClientConnectionsData();
        connectionsInfo.forEach((connection) => {
          vpnClientConnectionsData.latency += connection.latency / connectionsInfo.length;
          vpnClientConnectionsData.uploadSpeed += connection.upload_speed / connectionsInfo.length;
          vpnClientConnectionsData.downloadSpeed += connection.download_speed / connectionsInfo.length;
          vpnClientConnectionsData.totalUploaded += connection.bandwidth_sent;
          vpnClientConnectionsData.totalDownloaded += connection.bandwidth_received;
          if (connection.error) {
            vpnClientConnectionsData.error = connection.error;
          }
          if (connection.connection_duration > vpnClientConnectionsData.connectionDuration) {
            vpnClientConnectionsData.connectionDuration = connection.connection_duration;
          }
        });
        if (!this.connectionHistoryPk || this.connectionHistoryPk !== vpnClientData.serverPk) {
          this.connectionHistoryPk = vpnClientData.serverPk;
          this.uploadSpeedHistory = [0, 0, 0, 0, 0, 0, 0, 0, 0, 0];
          this.downloadSpeedHistory = [0, 0, 0, 0, 0, 0, 0, 0, 0, 0];
          this.latencyHistory = [0, 0, 0, 0, 0, 0, 0, 0, 0, 0];
        }
        vpnClientConnectionsData.latency = Math.round(vpnClientConnectionsData.latency);
        vpnClientConnectionsData.uploadSpeed = Math.round(vpnClientConnectionsData.uploadSpeed);
        vpnClientConnectionsData.downloadSpeed = Math.round(vpnClientConnectionsData.downloadSpeed);
        vpnClientConnectionsData.totalUploaded = Math.round(vpnClientConnectionsData.totalUploaded);
        vpnClientConnectionsData.totalDownloaded = Math.round(vpnClientConnectionsData.totalDownloaded);
        this.uploadSpeedHistory.splice(0, 1);
        this.uploadSpeedHistory.push(vpnClientConnectionsData.uploadSpeed);
        vpnClientConnectionsData.uploadSpeedHistory = this.uploadSpeedHistory;
        this.downloadSpeedHistory.splice(0, 1);
        this.downloadSpeedHistory.push(vpnClientConnectionsData.downloadSpeed);
        vpnClientConnectionsData.downloadSpeedHistory = this.downloadSpeedHistory;
        this.latencyHistory.splice(0, 1);
        this.latencyHistory.push(vpnClientConnectionsData.latency);
        vpnClientConnectionsData.latencyHistory = this.latencyHistory;
        vpnClientData.connectionData = vpnClientConnectionsData;
      }
      return vpnClientData;
    }));
  }
  /**
   * Gets the required data from the app properties.
   */
  processAppData(appData) {
    const vpnClientData = new VpnClientAppData();
    vpnClientData.running = appData.status !== 0 && appData.status !== 2;
    vpnClientData.connectionDuration = appData.connection_duration;
    vpnClientData.appState = AppState.Stopped;
    if (vpnClientData.running) {
      if (appData.detailed_status === AppState.Connecting || appData.status === 3) {
        vpnClientData.appState = AppState.Connecting;
      } else if (appData.detailed_status === AppState.Running) {
        vpnClientData.appState = AppState.Running;
      } else if (appData.detailed_status === AppState.ShuttingDown) {
        vpnClientData.appState = AppState.ShuttingDown;
      } else if (appData.detailed_status === AppState.Reconnecting) {
        vpnClientData.appState = AppState.Reconnecting;
      }
    } else if (appData.status === 2) {
      vpnClientData.lastErrorMsg = appData.detailed_status;
      if (!vpnClientData.lastErrorMsg) {
        vpnClientData.lastErrorMsg = this.translateService.instant("vpn.status-page.unknown-error");
      }
    }
    vpnClientData.killswitch = false;
    if (appData.args && appData.args.length > 0) {
      for (let i = 0; i < appData.args.length; i++) {
        if (appData.args[i] === "-srv" && i + 1 < appData.args.length) {
          vpnClientData.serverPk = appData.args[i + 1];
        }
        if (appData.args[i].toLowerCase().includes("-killswitch")) {
          vpnClientData.killswitch = appData.args[i].toLowerCase().includes("true");
        }
        if (appData.args[i].toLowerCase().includes("-dns")) {
          vpnClientData.dns = appData.args[i + 1];
        }
      }
    }
    return vpnClientData;
  }
  /**
   * Sends an update about the state of the VPN. It automatically sets the serviceState and
   * busy properties of currentEventData.
   */
  sendUpdate() {
    this.currentEventData.serviceState = this.lastServiceState;
    this.currentEventData.busy = this.working;
    this.currentEventData.publicIp = this.visorPublicIp;
    this.currentEventData.countryCode = this.visorCountryCode;
    this.currentEventData.countryName = this.visorCountryName;
    this.stateSubject.next(this.currentEventData);
  }
  static {
    this.ctorParameters = () => [
      { type: ApiService },
      { type: AppsService },
      { type: Router },
      { type: VpnSavedDataService },
      { type: HttpClient },
      { type: SnackbarService },
      { type: TranslateService }
    ];
  }
};
VpnClientService = __decorate2([
  Injectable({
    providedIn: "root"
  })
], VpnClientService);

// src/app/services/vpn-client.service.spec.ts
describe("VpnClientService", () => {
  let vpnSavedDataService;
  let translateService;
  let service;
  const build = () => new VpnClientService(
    {},
    // apiService
    {},
    // appsService
    {},
    // router
    vpnSavedDataService,
    {},
    // http
    {},
    // snackbarService
    translateService
  );
  beforeEach(() => {
    vpnSavedDataService = { currentServer: { pk: "SERVER_PK" } };
    translateService = { instant: (key) => key };
    service = build();
  });
  describe("checkNewPk", () => {
    const setState = (working, state) => {
      service.working = working;
      service.lastServiceState = state;
    };
    it("returns Busy while the service is working", () => {
      setState(true, VpnServiceStates.Off);
      expect(service.checkNewPk("ANY")).toBe(CheckPkResults.Busy);
    });
    it("returns SamePkRunning when running against the current server", () => {
      setState(false, VpnServiceStates.Running);
      expect(service.checkNewPk("SERVER_PK")).toBe(CheckPkResults.SamePkRunning);
    });
    it("returns MustStop when running against a different server", () => {
      setState(false, VpnServiceStates.Running);
      expect(service.checkNewPk("OTHER_PK")).toBe(CheckPkResults.MustStop);
    });
    it("returns SamePkStopped when stopped on the already-selected server", () => {
      setState(false, VpnServiceStates.Off);
      expect(service.checkNewPk("SERVER_PK")).toBe(CheckPkResults.SamePkStopped);
    });
    it("returns Ok when stopped and a new server is selected", () => {
      setState(false, VpnServiceStates.Off);
      expect(service.checkNewPk("OTHER_PK")).toBe(CheckPkResults.Ok);
    });
  });
  describe("processAppData", () => {
    const process = (appData) => service.processAppData(appData);
    it("treats status 0 (stopped) as not running", () => {
      const data = process({ status: 0, detailed_status: "" });
      expect(data.running).toBeFalse();
      expect(data.appState).toBe(AppState.Stopped);
    });
    it("maps a running app with detailed_status Running", () => {
      const data = process({ status: 1, detailed_status: AppState.Running });
      expect(data.running).toBeTrue();
      expect(data.appState).toBe(AppState.Running);
    });
    it("maps status 3 to Connecting", () => {
      const data = process({ status: 3, detailed_status: "" });
      expect(data.running).toBeTrue();
      expect(data.appState).toBe(AppState.Connecting);
    });
    it("captures the error message from a status 2 (error) app", () => {
      const data = process({ status: 2, detailed_status: "handshake failed" });
      expect(data.running).toBeFalse();
      expect(data.lastErrorMsg).toBe("handshake failed");
    });
    it("falls back to a translated message when a status 2 app has no detail", () => {
      const data = process({ status: 2, detailed_status: "" });
      expect(data.lastErrorMsg).toBe("vpn.status-page.unknown-error");
    });
    it("parses server pk, killswitch and dns from the app args", () => {
      const data = process({
        status: 1,
        detailed_status: AppState.Running,
        connection_duration: 120,
        args: ["-srv", "ARG_PK", "-killswitch=true", "-dns", "1.1.1.1"]
      });
      expect(data.serverPk).toBe("ARG_PK");
      expect(data.killswitch).toBeTrue();
      expect(data.dns).toBe("1.1.1.1");
      expect(data.connectionDuration).toBe(120);
    });
    it("defaults killswitch to false when the arg is absent", () => {
      const data = process({ status: 1, detailed_status: AppState.Running, args: ["-srv", "ARG_PK"] });
      expect(data.killswitch).toBeFalse();
    });
  });
});
//# debugId=682784a7-c5c0-5521-8927-c97c1fda84b1
//# sourceMappingURL=spec-app-services-vpn-client.service.spec.js.map
