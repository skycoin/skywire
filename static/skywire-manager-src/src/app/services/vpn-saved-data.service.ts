import { Injectable } from '@angular/core';
import { ReplaySubject, Observable } from 'rxjs';
import { Router } from '@angular/router';

import { VpnServer } from './vpn-client-discovery.service';
import { ManualVpnServerData } from '../components/vpn/pages/server-list/add-vpn-server/add-vpn-server.component';

export enum ServerFlags {
  None = 'None',
  Favorite = 'Favorite',
  Blocked = 'Blocked',
}

export interface LocalServerData {
  countryCode: string;
  name: string;
  customName: string;
  pk: string;
  lastUsed: number;
  notInDiscovery: boolean;
  inHistory: boolean;
  flag: ServerFlags;
  lastTimeUsedWithPassword: boolean;
  location: string;
  personalNote: string;
  note: string;
}

export interface SavedServersData {
  version: number;
  serverList: LocalServerData[];
  selectedServerPk: string;
}

@Injectable({
  providedIn: 'root'
})
export class VpnSavedDataService {
  private readonly maxHistoryElements = 30;

  private readonly savedServersStorageKey = 'VpnServers';
  private readonly checkIpSettingStorageKey = 'VpnGetIp';

  private currentServerPk: string;

  private serversMap = new Map<string, LocalServerData>();
  private savedDataVersion = 0;

  private currentServerSubject = new ReplaySubject<LocalServerData>(1);
  private historySubject = new ReplaySubject<LocalServerData[]>(1);
  private favoritesSubject = new ReplaySubject<LocalServerData[]>(1);
  private blockedSubject = new ReplaySubject<LocalServerData[]>(1);

  constructor(private router: Router) {}

  initialize() {
    this.serversMap = new Map<string, LocalServerData>();

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

    this.launchEvents();
  }

  get currentServer(): LocalServerData {
    return this.serversMap.get(this.currentServerPk);
  }
  get currentServerObservable(): Observable<LocalServerData> {
    return this.currentServerSubject.asObservable();
  }

  get history(): Observable<LocalServerData[]> {
    return this.historySubject.asObservable();
  }
  get favorites(): Observable<LocalServerData[]> {
    return this.favoritesSubject.asObservable();
  }
  get blocked(): Observable<LocalServerData[]> {
    return this.blockedSubject.asObservable();
  }

  getSavedVersion(pk: string, updateFromPersistentStorage: boolean) {
    if (updateFromPersistentStorage) {
      this.checkIfDataWasChanged();
    }

    return this.serversMap.get(pk);
  }

  getCheckIpSetting(): boolean {
    const val = localStorage.getItem(this.checkIpSettingStorageKey);
    if (val === null || val === undefined) {
      return true;
    }

    return val !== 'false';
  }

  setCheckIpSetting(value: boolean) {
    localStorage.setItem(this.checkIpSettingStorageKey, value ? 'true' : 'false');
  }

  updateFromDiscovery(serverList: VpnServer[]) {
    this.checkIfDataWasChanged();
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

  updateServer(server: LocalServerData) {
    this.serversMap.set(server.pk, server);
    this.cleanServers();
    this.saveData();
  }

  processFromDiscovery(newServer: VpnServer): LocalServerData {
    this.checkIfDataWasChanged();

    const retrievedServer = this.serversMap.get(newServer.pk);
    if (retrievedServer) {
      retrievedServer.name = newServer.name;
      retrievedServer.location = newServer.location;
      retrievedServer.notInDiscovery = false;
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
      notInDiscovery: false,
      inHistory: false,
      flag: ServerFlags.None,
      lastTimeUsedWithPassword: false,
      location: newServer.location,
      personalNote: null,
      note: newServer.note,
    };
  }

  processFromManual(newServer: ManualVpnServerData): LocalServerData {
    this.checkIfDataWasChanged();

    const retrievedServer = this.serversMap.get(newServer.pk);
    if (retrievedServer) {
      retrievedServer.customName = newServer.name;
      retrievedServer.personalNote = newServer.note;

      this.saveData();

      return retrievedServer;
    }

    return {
      countryCode: 'zz',
      name: '',
      customName: newServer.name,
      pk: newServer.pk,
      lastUsed: 0,
      notInDiscovery: true,
      inHistory: false,
      flag: ServerFlags.None,
      lastTimeUsedWithPassword: false,
      location: '',
      personalNote: newServer.note,
      note: '',
    };
  }

  changeFlag(server: LocalServerData, flag: ServerFlags) {
    this.checkIfDataWasChanged();

    const retrievedServer = this.serversMap.get(server.pk);
    if (retrievedServer) {
      server = retrievedServer;
    }

    if (server.flag === flag) {
      return;
    }

    server.flag = flag;
    this.serversMap.set(server.pk, server);
    this.cleanServers();
    this.saveData();
  }

  removeFromHistory(pk: string) {
    this.checkIfDataWasChanged();

    const retrievedServer = this.serversMap.get(pk);
    if (!retrievedServer || !retrievedServer.inHistory) {
      return;
    }

    retrievedServer.inHistory = false;
    this.cleanServers();
    this.saveData();
  }

  modifyCurrentServer(newServer: LocalServerData) {
    this.checkIfDataWasChanged();

    if (newServer.pk === this.currentServerPk) {
      return;
    }

    this.serversMap.set(newServer.pk, newServer);
    this.updateCurrentServerPk(newServer.pk);
    this.cleanServers();
    this.saveData();
  }

  compareCurrentServer(pk: string) {
    this.checkIfDataWasChanged();

    if (pk) {
      if (!this.currentServerPk || this.currentServerPk !== pk) {
        this.currentServerPk = pk;

        const retrievedServer = this.serversMap.get(pk);
        if (!retrievedServer) {
          const server = this.processFromManual({pk: pk, password: ''});
          this.serversMap.set(server.pk, server);
          this.cleanServers();
        }

        this.saveData();

        this.currentServerSubject.next(this.currentServer);
      }
    }
  }

  updateHistory() {
    this.checkIfDataWasChanged();

    this.currentServer.lastUsed = Date.now();
    this.currentServer.inHistory = true;

    let historyList: LocalServerData[] = [];
    this.serversMap.forEach(server => {
      if (server.inHistory) {
        historyList.push(server);
      }
    });
    historyList = historyList.sort((a, b) => b.lastUsed - a.lastUsed);

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

  private saveData() {
    let lastSavedVersion = 0;

    const retrievedServers = localStorage.getItem(this.savedServersStorageKey);
    if (retrievedServers) {
      const servers: SavedServersData = JSON.parse(retrievedServers);
      lastSavedVersion = servers.version;
    }

    // Previous calls to checkIfDataWasChanged should prevent this from happening.
    if (lastSavedVersion !== this.savedDataVersion) {
      this.router.navigate(['vpn', 'unavailable'], { queryParams: {problem: 'storage'}});

      return;
    }

    this.savedDataVersion += 1;
    const data: SavedServersData = {
      version: this.savedDataVersion,
      serverList: Array.from(this.serversMap.values()),
      selectedServerPk: this.currentServerPk,
    };

    const dataToSave = JSON.stringify(data);
    localStorage.setItem(this.savedServersStorageKey, dataToSave);

    this.launchEvents();
  }

  private checkIfDataWasChanged() {
    let lastSavedVersion = 0;

    const retrievedServers = localStorage.getItem(this.savedServersStorageKey);
    if (retrievedServers) {
      const servers: SavedServersData = JSON.parse(retrievedServers);
      lastSavedVersion = servers.version;
    }

    if (lastSavedVersion !== this.savedDataVersion) {
      this.initialize();
    }
  }

  private launchEvents() {
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
   * Call only after the server is in the servers map.
   */
  private updateCurrentServerPk(pk: string) {
    this.currentServerPk = pk;
    this.currentServerSubject.next(this.currentServer);
  }
}
