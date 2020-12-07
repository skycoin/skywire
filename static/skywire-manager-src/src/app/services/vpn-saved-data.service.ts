import { Injectable } from '@angular/core';
import { ReplaySubject, Observable } from 'rxjs';

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
}

@Injectable({
  providedIn: 'root'
})
export class VpnSavedDataService {
  private readonly maxHistoryElements = 30;

  private readonly savedServersStorageKey = 'VpnServers';

  private readonly currentServerStorageKey = 'VpnServer';

  private currentServerPk: string;

  private serversMap = new Map<string, LocalServerData>();
  private savedDataVersion = 0;

  private currentServerSubject = new ReplaySubject<LocalServerData>(1);
  private historySubject = new ReplaySubject<LocalServerData[]>(1);
  private favoritesSubject = new ReplaySubject<LocalServerData[]>(1);
  private blockedSubject = new ReplaySubject<LocalServerData[]>(1);

  constructor() {}

  initialize() {
    this.serversMap = new Map<string, LocalServerData>();

    const retrievedServers = localStorage.getItem(this.savedServersStorageKey);
    if (retrievedServers) {
      const servers: SavedServersData = JSON.parse(retrievedServers);
      servers.serverList.forEach(server => {
        this.serversMap.set(server.pk, server);
      });

      this.savedDataVersion = servers.version;
    }

    const currentServerPk = localStorage.getItem(this.currentServerStorageKey);
    if (currentServerPk) {
      this.updateCurrentServerPk(currentServerPk);
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

  getSavedVersion(pk: string) {
    return this.serversMap.get(pk);
  }

  processFromDiscovery(newServer: VpnServer): LocalServerData {
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
    const retrievedServer = this.serversMap.get(newServer.pk);
    if (retrievedServer) {
      // IMPORTANT: if more data is added manually, the saved data may have to be updated, like
      // it is done in processFromDiscovery().
      return retrievedServer;
    }

    return {
      countryCode: 'zz',
      name: '',
      pk: newServer.pk,
      lastUsed: 0,
      notInDiscovery: true,
      inHistory: false,
      flag: ServerFlags.None,
      lastTimeUsedWithPassword: false,
      location: '',
      personalNote: null,
      note: '',
    };
  }

  changeFlag(server: LocalServerData, flag: ServerFlags) {
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
    const retrievedServer = this.serversMap.get(pk);
    if (!retrievedServer || !retrievedServer.inHistory) {
      return;
    }

    retrievedServer.inHistory = false;
    this.cleanServers();
    this.saveData();
  }

  modifyCurrentServer(newServer: LocalServerData) {
    if (newServer.pk === this.currentServerPk) {
      return;
    }

    this.serversMap.set(newServer.pk, newServer);
    this.updateCurrentServerPk(newServer.pk);
    this.cleanServers();
    this.saveData();
  }

  compareCurrentServer(pk: string) {
    if (pk) {
      if (!this.currentServerPk || this.currentServerPk !== pk) {
        this.updateCurrentServerPk(pk);

        const retrievedServer = this.serversMap.get(pk);
        if (!retrievedServer) {
          const server = this.processFromManual({pk: pk, password: ''});
          this.serversMap.set(server.pk, server);
          this.cleanServers();
        }

        this.saveData();
      }
    }
  }

  updateHistory() {
    this.currentServer.lastUsed = Date.now();
    this.currentServer.inHistory = true;

    let historyList: LocalServerData[] = [];
    this.serversMap.forEach(server => {
      if (server.inHistory) {
        historyList.push(server);
      }
    });
    historyList = historyList.sort((a, b) => a.lastUsed - b.lastUsed);

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
      if (!server.inHistory && server.flag === ServerFlags.None && server.pk !== this.currentServerPk) {
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

    if (lastSavedVersion !== this.savedDataVersion) {
      this.initialize();

      return;
    }

    this.savedDataVersion += 1;
    const data: SavedServersData = {
      version: this.savedDataVersion,
      serverList: Array.from(this.serversMap.values())
    };

    const dataToSave = JSON.stringify(data);
    localStorage.setItem(this.savedServersStorageKey, dataToSave);

    localStorage.setItem(this.currentServerStorageKey, this.currentServerPk);

    this.launchEvents();
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
