import { Injectable } from '@angular/core';
import { ReplaySubject, Observable } from 'rxjs';
import { Router } from '@angular/router';

import { VpnServer } from './vpn-client-discovery.service';
import { ManualVpnServerData } from '../components/vpn/pages/vpn-server-list/add-vpn-server/add-vpn-server.component';

/**
 * Special conditions a server may have.
 */
export enum ServerFlags {
  None = 'None',
  /**
   * The server is in the favorites list.
   */
  Favorite = 'Favorite',
  /**
   * The server is in the blocked list.
   */
  Blocked = 'Blocked',
}

/**
 * Data for representing a VPN server locally.
 */
export interface LocalServerData {
  /**
   * 2 letter code of the country the server is in.
   */
  countryCode: string;
  /**
   * Sever name, obtained from the discovery service.
   */
  name: string;
  /**
   * Custom name set by the user.
   */
  customName: string;
  /**
   * Public key.
   */
  pk: string;
  /**
   * Last moment in which the VPN was connected to the server.
   */
  lastUsed: number;
  /**
   * If the server is in the history of recently used servers.
   */
  inHistory: boolean;
  /**
   * Special condition the server may have.
   */
  flag: ServerFlags;
  /**
   * Location of the server, obtained from the discovery service.
   */
  location: string;
  /**
   * Note with information about the server, obtained from the discovery service.
   */
  note: string;
  /**
   * Personal note added by the user.
   */
  personalNote: string;
  /**
   * If the last time the server was used it was used with a password.
   */
  usedWithPassword: boolean;
  /**
   * If the server was entered manually, at least one time.
   */
  enteredManually: boolean;
}

/**
 * Data about the server list and the currently selected server that is saved by
 * VpnSavedDataService to persistent storage.
 */
interface SavedServersData {
  /**
   * Version of the saved data, used to check if the data was updated.
   */
  version: number;
  /**
   * Server list.
   */
  serverList: LocalServerData[];
  /**
   * Public key of the currently selected server.
   */
  selectedServerPk: string;
}

/**
 * Options for how to show the VPN data transmission stats.
 */
export enum DataUnits {
  BitsSpeedAndBytesVolume = 'BitsSpeedAndBytesVolume',
  OnlyBytes = 'OnlyBytes',
  OnlyBits = 'OnlyBits',
}

/**
 * Manages the local data of the VPN client. Regarding the saved servers, the service only
 * maintains in local storage the servers which are currently selected, in a special server lists
 * or have personal data, so it is normal not to receive a response when consulting the pk of
 * a server not that does not meet any of those characteristics.
 */
@Injectable({
  providedIn: 'root'
})
export class VpnSavedDataService {
  // Max number of elements the server history can have.
  private readonly maxHistoryElements = 30;

  // Local storage key for the server list and the PK of the currently selected server.
  private readonly savedServersStorageKey = 'VpnServers';
  // Local storage key for the setting allowing to get the local IP.
  private readonly checkIpSettingStorageKey = 'VpnGetIp';
  // Local storage key for the data units setting.
  private readonly dataUnitsSettingStorageKey = 'VpnDataUnits';

  // Public key of the currently selected server.
  private currentServerPk: string;
  // Map with all the locally saved VPN servers, accessible via public key.
  private serversMap = new Map<string, LocalServerData>();
  // Version of the saved server list, as expected by this service. If the number has been changed
  // unexpectedly in the local storage, it means the user made changes using another instance of
  // the app.
  private savedDataVersion = 0;

  private currentServerSubject = new ReplaySubject<LocalServerData>(1);
  private historySubject = new ReplaySubject<LocalServerData[]>(1);
  private favoritesSubject = new ReplaySubject<LocalServerData[]>(1);
  private blockedSubject = new ReplaySubject<LocalServerData[]>(1);

  constructor(private router: Router) {}

  /**
   * Loads the data from local storage and updates all the vars, so the service can work correctly.
   */
  initialize() {
    this.serversMap = new Map<string, LocalServerData>();

    // Get the saved servers and the pk of the currently selected one.
    const retrievedServers = localStorage.getItem(this.savedServersStorageKey);
    if (retrievedServers) {
      const servers: SavedServersData = JSON.parse(retrievedServers);
      servers.serverList.forEach(server => {
        this.serversMap.set(server.pk, server);
      });

      this.savedDataVersion = servers.version;

      if (servers.selectedServerPk) {
        this.updateCurrentServerPk(servers.selectedServerPk);
      }
    }

    // Launch the events with the updated server lists.
    this.launchListEvents();
  }

  /**
   * Currently selected server.
   */
  get currentServer(): LocalServerData {
    return this.serversMap.get(this.currentServerPk);
  }
  /**
   * Observable which emits the currently selected server after each change.
   */
  get currentServerObservable(): Observable<LocalServerData> {
    return this.currentServerSubject.asObservable();
  }

  /**
   * Used servers history. If any of the returned values is changed, the updateServer function
   * must be called after that, with the modified server.
   */
  get history(): Observable<LocalServerData[]> {
    return this.historySubject.asObservable();
  }
  /**
   * Favorite servers list. If any of the returned values is changed, the updateServer function
   * must be called after that, with the modified server.
   */
  get favorites(): Observable<LocalServerData[]> {
    return this.favoritesSubject.asObservable();
  }
  /**
   * Blocked servers list. If any of the returned values is changed, the updateServer function
   * must be called after that, with the modified server.
   */
  get blocked(): Observable<LocalServerData[]> {
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
  getSavedVersion(pk: string, updateFromPersistentStorage: boolean) {
    if (updateFromPersistentStorage) {
      this.checkIfDataWasChanged();
    }

    return this.serversMap.get(pk);
  }

  /**
   * Returns if the app should check the current local IP (true) or not (false).
   * If the user has not changed the setting, it returns true by default.
   */
  getCheckIpSetting(): boolean {
    const val = localStorage.getItem(this.checkIpSettingStorageKey);
    if (val === null || val === undefined) {
      return true;
    }

    return val !== 'false';
  }

  /**
   * Sets if the app should check the current local IP (true) or not (false).
   */
  setCheckIpSetting(value: boolean) {
    localStorage.setItem(this.checkIpSettingStorageKey, value ? 'true' : 'false');
  }

  /**
   * Returs the data units that must be shown in the UI. If the user has not changed
   * the setting, it returns DataUnits.BitsSpeedAndBytesVolume by default.
   */
   getDataUnitsSetting(): DataUnits {
    const val = localStorage.getItem(this.dataUnitsSettingStorageKey);
    if (val === null || val === undefined) {
      return DataUnits.BitsSpeedAndBytesVolume;
    }

    return val as DataUnits;
  }

  /**
   * Sets the data units that must be shown in the UI.
   */
   setDataUnitsSetting(value: DataUnits) {
    localStorage.setItem(this.dataUnitsSettingStorageKey, value);
  }

  /**
   * Updates the data of the locally saved servers with the data obtained from the
   * discovery service.
   * @param serverList Servers obtained from the discovery service.
   */
  updateFromDiscovery(serverList: VpnServer[]) {
    // Update the local data, if needed.
    this.checkIfDataWasChanged();

    // Check all servers obtained from the discovery service and update the data of the
    // servers that have already been saved.
    serverList.forEach(server => {
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
  updateServer(server: LocalServerData) {
    this.serversMap.set(server.pk, server);
    this.cleanServers();
    this.saveData();
  }

  /**
   * Creates a LocalServerData instance from a VpnServer instance. If the server has already
   * been saved, the saved version is updated with the provided data and returned.
   */
  processFromDiscovery(newServer: VpnServer): LocalServerData {
    // Update the local data, if needed.
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
      enteredManually: false,
      usedWithPassword: false,
    };
  }

  /**
   * Creates a LocalServerData instance from a ManualVpnServerData instance. If server the has already
   * been saved, the saved version is updated with the provided data and returned.
   */
  processFromManual(newServer: ManualVpnServerData): LocalServerData {
    // Update the local data, if needed.
    this.checkIfDataWasChanged();

    const retrievedServer = this.serversMap.get(newServer.pk);
    if (retrievedServer) {
      retrievedServer.customName = newServer.name;
      retrievedServer.personalNote = newServer.note;
      retrievedServer.enteredManually = true;

      this.saveData();

      return retrievedServer;
    }

    return {
      countryCode: 'zz',
      name: '',
      customName: newServer.name,
      pk: newServer.pk,
      lastUsed: 0,
      inHistory: false,
      flag: ServerFlags.None,
      location: '',
      personalNote: newServer.note,
      note: '',
      enteredManually: true,
      usedWithPassword: false,
    };
  }

  /**
   * Changes the flag property of a server and saves the changes.
   * If the flag is the same, nothing happens.
   */
  changeFlag(server: LocalServerData, flag: ServerFlags) {
    // Update the local data, if needed.
    this.checkIfDataWasChanged();

    const retrievedServer = this.serversMap.get(server.pk);
    if (retrievedServer) {
      server = retrievedServer;
    }

    if (server.flag === flag) {
      return;
    }
    server.flag = flag;

    // Add the server to the saved servers list, if needed.
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
  removeFromHistory(pk: string) {
    // Update the local data, if needed.
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
  modifyCurrentServer(newServer: LocalServerData) {
    // Update the local data, if needed.
    this.checkIfDataWasChanged();

    if (newServer.pk === this.currentServerPk) {
      return;
    }

    // Add the server to the saved servers list, if needed.
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
  compareCurrentServer(pk: string) {
    // Update the local data, if needed.
    this.checkIfDataWasChanged();

    if (pk) {
      if (!this.currentServerPk || this.currentServerPk !== pk) {
        this.currentServerPk = pk;

        const retrievedServer = this.serversMap.get(pk);
        if (!retrievedServer) {
          const server = this.processFromManual({pk: pk});
          this.serversMap.set(server.pk, server);
          this.cleanServers();
        }

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
    // Update the local data, if needed.
    this.checkIfDataWasChanged();

    this.currentServer.lastUsed = Date.now();
    this.currentServer.inHistory = true;

    // Make a list with the servers in the history and sort it by usage date.
    let historyList: LocalServerData[] = [];
    this.serversMap.forEach(server => {
      if (server.inHistory) {
        historyList.push(server);
      }
    });
    historyList = historyList.sort((a, b) => b.lastUsed - a.lastUsed);

    // Remove from the history the old servers.
    let historyElementsFound = 0;
    historyList.forEach(server => {
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
  private cleanServers() {
    const unneeded: string[] = [];
    this.serversMap.forEach(server => {
      if (
        !server.inHistory &&
        server.flag === ServerFlags.None &&
        server.pk !== this.currentServerPk &&
        !server.customName &&
        !server.personalNote
      ) {
        unneeded.push(server.pk);
      }
    });

    unneeded.forEach(pk => {
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
  private saveData() {
    // Check the version of the data saved in persistent storage.
    let lastSavedVersion = 0;
    const retrievedServers = localStorage.getItem(this.savedServersStorageKey);
    if (retrievedServers) {
      const servers: SavedServersData = JSON.parse(retrievedServers);
      lastSavedVersion = servers.version;
    }

    // If the version is not the expected one, the operation must be cancelled to avoid problems.
    // Previous calls to checkIfDataWasChanged should prevent this from happening.
    if (lastSavedVersion !== this.savedDataVersion) {
      this.router.navigate(['vpn', 'unavailable'], { queryParams: {problem: 'storage'}});

      return;
    }

    // Save the data.
    this.savedDataVersion += 1;
    const data: SavedServersData = {
      version: this.savedDataVersion,
      serverList: Array.from(this.serversMap.values()),
      selectedServerPk: this.currentServerPk,
    };
    const dataToSave = JSON.stringify(data);
    localStorage.setItem(this.savedServersStorageKey, dataToSave);

    // Update the events.
    this.launchListEvents();
  }

  /**
   * Check if the data saved to persistent storage is more recent than the one currently loaded
   * in the app. If that is the case, the server list and the PK of the selected server are
   * updated with the data obtained from persistent storage. This allows to avoid problems when
   * more than one instance of the app is running at the same time.
   */
  private checkIfDataWasChanged() {
    let lastSavedVersion = 0;

    const retrievedServers = localStorage.getItem(this.savedServersStorageKey);
    if (retrievedServers) {
      const servers: SavedServersData = JSON.parse(retrievedServers);
      lastSavedVersion = servers.version;
    }

    // Reload the data, if needed.
    if (lastSavedVersion !== this.savedDataVersion) {
      this.initialize();
    }
  }

  /**
   * Launches the server list events, with the most recent values.
   */
  private launchListEvents() {
    const history: LocalServerData[] = [];
    const favorites: LocalServerData[] = [];
    const blocked: LocalServerData[] = [];

    this.serversMap.forEach(server => {
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
  private updateCurrentServerPk(pk: string) {
    this.currentServerPk = pk;
    this.currentServerSubject.next(this.currentServer);
  }
}
