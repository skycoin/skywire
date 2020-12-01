import { Injectable } from '@angular/core';
import { Router } from '@angular/router';
import { Observable, Subscription, of, BehaviorSubject, concat, throwError, ReplaySubject } from 'rxjs';
import { mergeMap, delay, retryWhen, take, catchError } from 'rxjs/operators';

import { ApiService } from './api.service';
import { AppsService } from './apps.service';
import { VpnServer } from './vpn-client-discovery.service';
import { ManualVpnServerData } from '../components/vpn/pages/server-list/add-vpn-server/add-vpn-server.component';

export class BackendState {
  updateDate: number = Date.now();
  lastError: any;
  vpnClient: VpnClient;
  serviceState: VpnStates;
  busy: boolean;
}

export class VpnClient {
  running: boolean;
  serverPk: string;
}

export enum VpnStates {
  PerformingInitialCheck = 1,
  Off = 10,
  Starting = 20,
  Running = 100,
  Disconnecting = 200,
}

export enum CheckPkResults {
  Busy = 1,
  Ok = 2,
  MustStop = 3,
  SamePkRunning = 4,
  SamePkStopped = 5,
}

export interface HistoryEntry {
  name: string;
  pk: string;
  enteredManually: boolean;
  location?: string;
  personalNote?: string;
  note?: string;
  hasPassword?: boolean;
}

@Injectable({
  providedIn: 'root'
})
export class VpnClientService {
  readonly vpnClientAppName = 'vpn-client';

  private readonly standardWaitTime = 2000;
  private readonly maxHistoryElements = 30;

  private readonly historyStorageKey = 'VpnHistory';
  private readonly favoritesStorageKey = 'VpnFavorites';
  private readonly blockedStorageKey = 'VpnBlocked';
  private readonly currentServerStorageKey = 'VpnServer';

  private nodeKey: string;
  private stateSubject = new BehaviorSubject<BackendState>(null);
  private dataSubscription: Subscription;
  private continuousUpdateSubscription: Subscription;

  private currentEventData: BackendState;
  private lastState: VpnStates;
  private working = true;

  private requestedServer: HistoryEntry = null;
  private requestedPassword: string = null;

  private currentServer: HistoryEntry;

  private history: HistoryEntry[] = [];
  private historyMap: Map<string, HistoryEntry>;
  private favorites: HistoryEntry[] = [];
  private favoritesMap: Map<string, HistoryEntry>;
  private blocked: HistoryEntry[] = [];
  private blockedMap: Map<string, HistoryEntry>;

  private historySubject = new ReplaySubject<HistoryEntry[]>(1);

  constructor(
    private apiService: ApiService,
    private appsService: AppsService,
    private router: Router,
  ) {
    this.currentEventData = new BackendState();
    this.currentEventData.vpnClient = null;
    this.currentEventData.busy = true;

    this.lastState = VpnStates.PerformingInitialCheck;

    const retrievedHistory = localStorage.getItem(this.historyStorageKey);
    if (retrievedHistory) {
      this.history = JSON.parse(retrievedHistory);
      this.history.forEach(server => {
        this.historyMap.set(server.pk, server);
      });
    }
    this.historySubject.next(this.history);

    const retrievedFavorites = localStorage.getItem(this.favoritesStorageKey);
    if (retrievedFavorites) {
      this.favorites = JSON.parse(retrievedFavorites);
      this.favorites.forEach(server => {
        this.favoritesMap.set(server.pk, server);
      });
    }

    const retrievedBlocked = localStorage.getItem(this.blockedStorageKey);
    if (retrievedBlocked) {
      this.blocked = JSON.parse(retrievedBlocked);
      this.blocked.forEach(server => {
        this.blockedMap.set(server.pk, server);
      });
    }

    const retrievedServer = localStorage.getItem(this.currentServerStorageKey);
    if (retrievedServer) {
      this.currentServer = JSON.parse(retrievedHistory);
    }
  }

  initialize(nodeKey: string) {
    if (nodeKey) {
      if (!this.nodeKey) {
        this.nodeKey = nodeKey;

        this.performInitialCheck();
      }
    }
  }

  get backendState(): Observable<BackendState> {
    return this.stateSubject.asObservable();
  }

  get serversHistory(): Observable<HistoryEntry[]> {
    return this.historySubject.asObservable();
  }

  updateData() {
    this.continuallyUpdateData(0);
  }

  start(): boolean {
    if (!this.working && this.lastState < 20) {
      this.currentEventData.lastError = null;

      this.changeAppState(true);

      return true;
    }

    return false;
  }

  stop(): boolean {
    if (!this.working && this.lastState >= 20 && this.lastState < 200) {
      this.changeAppState(false);

      return true;
    }

    return false;
  }

  changeServerUsingHistory(newServer: HistoryEntry): boolean {
    this.requestedServer = newServer;

    return this.changeServer();
  }

  changeServerUsingDiscovery(newServer: VpnServer): boolean {
    const previousData = this.getPreviouslySavedServerData(newServer.pk);

    this.requestedServer = {
      name: newServer.name,
      pk: newServer.pk,
      enteredManually: false,
      location: newServer.location,
      personalNote: previousData ? previousData.personalNote : null,
      note: newServer.note,
      hasPassword: false,
    };

    return this.changeServer();
  }

  changeServerManually(newServer: ManualVpnServerData): boolean {
    const previousData = this.getPreviouslySavedServerData(newServer.pk);

    this.requestedServer = {
      name: '',
      pk: newServer.pk,
      enteredManually: true,
      personalNote: previousData ? previousData.personalNote : null,
      hasPassword: !!newServer.password,
    };

    return this.changeServer();
  }

  markServerFromDiscovery(server: VpnServer, makeFavorite: boolean) {
    const previousData = this.getPreviouslySavedServerData(server.pk);

    // The data must be updated everywere.

    const serverData = {
      name: server.name,
      pk: server.pk,
      enteredManually: false,
      location: server.location,
      personalNote: previousData ? previousData.personalNote : null,
      note: server.note,
      hasPassword: false,
    };

    this.finishMarkingkServer(serverData, makeFavorite);
  }

  // Allow to make favorites from history.
  markServerFromHistory(server: HistoryEntry, makeFavorite: boolean) {
    this.finishMarkingkServer(server, makeFavorite);
  }

  private finishMarkingkServer(server: HistoryEntry, makeFavorite: boolean) {
    // The data must be updated before cancelling the operation.
    if (makeFavorite && this.favoritesMap.get(server.pk)) {
      return;
    } else if (!makeFavorite && this.blockedMap.get(server.pk)) {
      return;
    }

    if (makeFavorite) {
      if (this.favoritesMap.get(server.pk)) {
        return;
      }

      this.removeMarkedServer(server.pk, false);
    } else {
      if (this.blockedMap.get(server.pk)) {
        return;
      }

      this.removeMarkedServer(server.pk, true);
    }

    // The data must be updated everywere.

    if (makeFavorite) {
      this.favorites.push(server);
      this.favoritesMap.set(server.pk, server);
    } else if (!makeFavorite) {
      this.blocked.push(server);
      this.blockedMap.set(server.pk, server);
    }
  }

  removeMarkedServer(pk: string, removeFromFavorites: boolean) {
    if (removeFromFavorites) {
      if (this.favoritesMap.get(pk)) {
        this.favorites = this.favorites.filter(value => value.pk !== pk);
        this.favoritesMap.delete(pk);
      }
    } else {
      if (this.blockedMap.get(pk)) {
        this.blocked = this.blocked.filter(value => value.pk !== pk);
        this.blockedMap.delete(pk);
      }
    }
  }

  private getPreviouslySavedServerData(pk: string): HistoryEntry {
    if (this.currentServer.pk === pk) {
      return this.currentServer;
    }

    let result: HistoryEntry;

    if (this.historyMap.has(pk)) {
      result = this.historyMap.get(pk);
    } else if (this.favoritesMap.has(pk)) {
      result = this.favoritesMap.get(pk);
    } else if (this.blockedMap.has(pk)) {
      result = this.blockedMap.get(pk);
    }

    return result;
  }

  private changeServer(): boolean {
    if (!this.working) {
      if (!this.stop()) {
        this.processServerChange();
      }

      return true;
    }

    return false;
  }

  checkNewPk(newPk): CheckPkResults {
    if (this.working) {
      return CheckPkResults.Busy;
    } else if (this.lastState !== VpnStates.Off) {
      if (newPk === this.currentServer.pk) {
        return CheckPkResults.SamePkRunning;
      } else {
        return CheckPkResults.MustStop;
      }
    } else if (this.currentServer && newPk === this.currentServer.pk) {
      return CheckPkResults.SamePkStopped;
    }

    return CheckPkResults.Ok;
  }

  private processServerChange() {
    if (this.dataSubscription) {
      this.dataSubscription.unsubscribe();
    }

    const data = { pk: this.requestedServer.pk };
    if (this.requestedPassword) {
      data['passcode'] = this.requestedPassword;
    } else {
      data['passcode'] = '';
    }

    this.stopContinuallyUpdateData();
    this.sendUpdate();

    // TODO: react in case of errors.
    this.dataSubscription = this.appsService.changeAppSettings(
      this.nodeKey,
      this.vpnClientAppName,
      data,
    ).subscribe(
      () => {
        this.currentServer = this.requestedServer;
        this.saveCurrentServer();

        this.requestedServer = null;
        this.requestedPassword = null;
        this.working = false;

        if (this.currentEventData && this.currentEventData.vpnClient) {
          this.currentEventData.vpnClient.serverPk = data.pk;
        }

        this.start();
      }, () => {
        // More processing needed.
        this.requestedServer = null;
        this.requestedPassword = null;
      }
    );
  }

  private changeAppState(startApp: boolean) {
    if (this.working) {
      return;
    }

    const data = { status: 1 };

    if (startApp) {
      this.lastState = VpnStates.Starting;
    } else {
      this.lastState = VpnStates.Disconnecting;
      data.status = 0;
    }

    if (this.dataSubscription) {
      this.dataSubscription.unsubscribe();
    }

    this.stopContinuallyUpdateData();
    this.sendUpdate();

    this.dataSubscription = this.appsService.changeAppSettings(
      this.nodeKey,
      this.vpnClientAppName,
      data,
    ).pipe(
      catchError(err => {
        // If the response was an error, check the state of the backend, to know if the change
        // was made.
        return this.getBackendData().pipe(mergeMap(nodeInfo => {
          const appData = this.extractVpnAppData(nodeInfo);
          if (appData) {
            const vpnClientData = this.getVpnClientData(appData);
            if (startApp && vpnClientData.running) {
              return of(true);
            } else if (!startApp && !vpnClientData.running) {
              return of(true);
            }
          }

          return throwError(err);
        }));
      }),
      retryWhen(errors => concat(errors.pipe(delay(this.standardWaitTime), take(10)), throwError('')))
    ).subscribe(response => {
      this.working = false;

      if (startApp) {
        if (this.currentEventData && this.currentEventData.vpnClient) {
          this.currentEventData.vpnClient.running = true;
        }
        this.lastState = VpnStates.Running;

        this.updateHistory();
      } else {
        if (this.currentEventData && this.currentEventData.vpnClient) {
          this.currentEventData.vpnClient.running = false;
        }
        this.lastState = VpnStates.Off;
      }
      this.sendUpdate();
      this.updateData();

      if (!startApp && this.requestedServer) {
        this.processServerChange();
      }
    }, err => {
      if (this.lastState === VpnStates.Starting) {
        // TODO: process "Could not start".
      } else if (this.lastState === VpnStates.Disconnecting) {
        // TODO: process Could not stop.
      } else {
        // TODO: process Should not happen.
      }
    });
  }

  private performInitialCheck() {
    if (this.dataSubscription) {
      this.dataSubscription.unsubscribe();
    }

    this.dataSubscription = this.getBackendData().pipe(
      retryWhen(errors => concat(errors.pipe(delay(this.standardWaitTime), take(10)), throwError('')))
    ).subscribe(nodeInfo => {
      const appData = this.extractVpnAppData(nodeInfo);
      if (appData) {
        this.working = false;

        const vpnClientData = this.getVpnClientData(appData);
        this.conpareCurrentServer(vpnClientData.serverPk);

        if (vpnClientData.running) {
          this.lastState = VpnStates.Running;
        } else {
          this.lastState = VpnStates.Off;
        }

        this.currentEventData.vpnClient = vpnClientData;
        this.sendUpdate();

        this.continuallyUpdateData(this.standardWaitTime);
      } else {
        this.router.navigate(['vpn', 'unavailable']);
        this.nodeKey = null;
      }
    }, err => {
      this.router.navigate(['vpn', 'unavailable']);
      this.nodeKey = null;
    });
  }

  private getVpnClientData(appData: any): VpnClient {
    const vpnClientData = new VpnClient();
    vpnClientData.running = appData.status !== 0;

    if (appData.args && appData.args.length > 0) {
      for (let i = 0; i < appData.args.length; i++) {
        if (appData.args[i] === '-srv' && i + 1 < appData.args.length) {
          vpnClientData.serverPk = appData.args[i + 1];
        }
      }
    }

    return vpnClientData;
  }

  private extractVpnAppData(nodeInfo: any): any {
    let appData: any;

    if (nodeInfo && nodeInfo.apps && (nodeInfo.apps as any[]).length > 0) {
      (nodeInfo.apps as any[]).forEach(value => {
        if (value.name === this.vpnClientAppName) {
          appData = value;
        }
      });
    }

    return appData;
  }

  private continuallyUpdateData(delayMs: number) {
    if (this.working) {
      return;
    }

    if (this.continuousUpdateSubscription) {
      this.continuousUpdateSubscription.unsubscribe();
    }

    this.continuousUpdateSubscription = of(0).pipe(
      delay(delayMs),
      mergeMap(() => this.getBackendData()),
      retryWhen(errors => errors.pipe(delay(1000)))
    ).subscribe(nodeInfo => {
      const appData = this.extractVpnAppData(nodeInfo);
      if (appData) {
        const vpnClientData = this.getVpnClientData(appData);
        this.conpareCurrentServer(vpnClientData.serverPk);

        if (vpnClientData.running) {
          this.lastState = VpnStates.Running;
        } else {
          this.lastState = VpnStates.Off;
        }

        this.currentEventData.vpnClient = vpnClientData;
        this.sendUpdate();
      }

      this.continuallyUpdateData(this.standardWaitTime);
    });
  }

  private stopContinuallyUpdateData() {
    this.working = true;

    if (this.continuousUpdateSubscription) {
      this.continuousUpdateSubscription.unsubscribe();
    }
  }

  private getBackendData(): Observable<any> {
    return this.apiService.get(`visors/${this.nodeKey}`);
  }

  private sendUpdate() {
    this.currentEventData.serviceState = this.lastState;
    this.currentEventData.updateDate = Date.now();
    this.currentEventData.busy = this.working;
    this.stateSubject.next(this.currentEventData);
  }

  private updateHistory() {
    this.history = this.history.filter(value => value.pk !== this.currentServer.pk);
    this.history = [this.currentServer].concat(this.history);

    if (this.history.length > this.maxHistoryElements) {
      const itemsToRemove = this.history.length - this.maxHistoryElements;
      this.history.splice(this.history.length - itemsToRemove, itemsToRemove);
    }

    const dataToSave = JSON.stringify(this.history);
    localStorage.setItem(this.historyStorageKey, dataToSave);

    this.historySubject.next(this.history);
  }

  private saveCurrentServer() {
    const dataToSave = JSON.stringify(this.currentServer);
    localStorage.setItem(this.currentServerStorageKey, dataToSave);
  }

  private conpareCurrentServer(pk: string) {
    if (pk) {
      if (!this.currentServer || this.currentServer.pk !== pk) {
        this.currentServer = {
          name: '',
          pk: pk,
          enteredManually: true,
          hasPassword: false,
        };

        this.saveCurrentServer();
      }
    }
  }
}
