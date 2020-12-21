import { Injectable } from '@angular/core';
import { Router } from '@angular/router';
import { Observable, Subscription, of, BehaviorSubject, concat, throwError } from 'rxjs';
import { mergeMap, delay, retryWhen, take, catchError, map } from 'rxjs/operators';
import { HttpClient } from '@angular/common/http';

import { ApiService } from './api.service';
import { AppsService } from './apps.service';
import { VpnServer } from './vpn-client-discovery.service';
import { ManualVpnServerData } from '../components/vpn/pages/server-list/add-vpn-server/add-vpn-server.component';
import { VpnSavedDataService, LocalServerData } from './vpn-saved-data.service';

export enum AppState {
  Stopped = 'stopped',
  Connecting = 'Connecting',
  Running = 'Running',
  ShuttingDown = 'Shutting down',
  Reconnecting = 'Connection failed, reconnecting',
}

export class BackendState {
  updateDate: number = Date.now();
  appState: AppState;
  lastError: any;
  running: boolean;
  serviceState: VpnStates;
  busy: boolean;
  killswitch: boolean;
}

export class VpnClientAppData {
  running: boolean;
  serverPk: string;
  killswitch: boolean;
  appState: AppState;
}

export interface IpInfo {
  ip: string;
  country: string;
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

@Injectable({
  providedIn: 'root'
})
export class VpnClientService {
  readonly vpnClientAppName = 'vpn-client';

  private readonly standardWaitTime = 2000;

  private nodeKey: string;
  private stateSubject = new BehaviorSubject<BackendState>(null);
  private dataSubscription: Subscription;
  private continuousUpdateSubscription: Subscription;

  private currentEventData: BackendState;
  private lastState: VpnStates;
  private working = true;

  private requestedServer: LocalServerData = null;
  private requestedPassword: string = null;

  constructor(
    private apiService: ApiService,
    private appsService: AppsService,
    private router: Router,
    private vpnSavedDataService: VpnSavedDataService,
    private http: HttpClient,
  ) {
    this.currentEventData = new BackendState();
    this.currentEventData.busy = true;

    this.lastState = VpnStates.PerformingInitialCheck;
  }

  initialize(nodeKey: string) {
    if (nodeKey) {
      if (!this.nodeKey) {
        this.nodeKey = nodeKey;

        this.vpnSavedDataService.initialize();

        this.performInitialCheck();
      }
    }
  }

  get backendState(): Observable<BackendState> {
    return this.stateSubject.asObservable();
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

  getIp(): Observable<IpInfo> {
    let ip: string;

    return of(1).pipe(mergeMap(tmp => {
      if (!ip) {
        return this.http.request('GET', 'https://api.ipify.org?format=json');
      } else {
        return of(undefined);
      }
    }), mergeMap(data => {
      if (data && data['ip']) {
        ip = data['ip'];
      }

      if (ip) {
        return this.http.request('GET', 'https://ip2c.org/' + ip, { responseType: 'text' });
      } else {
        return of('-1');
      }
    }), retryWhen(errors => concat(errors.pipe(delay(2000), take(4)), throwError(ip))),
    map (response => {
      if (response === '-1') {
        return null;
      }

      let country: string;
      if (response) {
        const reply: string[] = response.split(';');

        if (reply.length === 4) {
          country = reply[3];
        }
      }

      const result: IpInfo = {
        ip: ip,
        country: country,
      };

      return result;
    }));
  }

  changeServerUsingHistory(newServer: LocalServerData): boolean {
    this.requestedServer = newServer;

    return this.changeServer();
  }

  changeServerUsingDiscovery(newServer: VpnServer): boolean {
    this.requestedServer = this.vpnSavedDataService.processFromDiscovery(newServer);

    return this.changeServer();
  }

  changeServerManually(newServer: ManualVpnServerData): boolean {
    this.requestedServer = this.vpnSavedDataService.processFromManual(newServer);

    return this.changeServer();
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
      if (newPk === this.vpnSavedDataService.currentServer.pk) {
        return CheckPkResults.SamePkRunning;
      } else {
        return CheckPkResults.MustStop;
      }
    } else if (this.vpnSavedDataService.currentServer && newPk === this.vpnSavedDataService.currentServer.pk) {
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
        this.vpnSavedDataService.modifyCurrentServer(this.requestedServer);

        this.requestedServer = null;
        this.requestedPassword = null;
        this.working = false;

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
        if (this.currentEventData) {
          this.currentEventData.running = true;
        }
        this.lastState = VpnStates.Running;

        this.vpnSavedDataService.updateHistory();
      } else {
        if (this.currentEventData) {
          this.currentEventData.running = false;
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
        this.vpnSavedDataService.compareCurrentServer(vpnClientData.serverPk);

        if (vpnClientData.running) {
          this.lastState = VpnStates.Running;
        } else {
          this.lastState = VpnStates.Off;
        }

        this.currentEventData.running = vpnClientData.running;
        this.currentEventData.killswitch = vpnClientData.killswitch;
        this.currentEventData.appState = vpnClientData.appState;
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

  private getVpnClientData(appData: any): VpnClientAppData {
    const vpnClientData = new VpnClientAppData();
    vpnClientData.running = appData.status !== 0;

    vpnClientData.appState = AppState.Stopped;
    if (appData.detailed_status === AppState.Connecting) {
      vpnClientData.appState = AppState.Connecting;
    } else if (appData.detailed_status === AppState.Running) {
      vpnClientData.appState = AppState.Running;
    } else if (appData.detailed_status === AppState.ShuttingDown) {
      vpnClientData.appState = AppState.ShuttingDown;
    } else if (appData.detailed_status === AppState.Reconnecting) {
      vpnClientData.appState = AppState.Reconnecting;
    }

    vpnClientData.killswitch = false;

    if (appData.args && appData.args.length > 0) {
      for (let i = 0; i < appData.args.length; i++) {
        if (appData.args[i] === '-srv' && i + 1 < appData.args.length) {
          vpnClientData.serverPk = appData.args[i + 1];
        }

        if (appData.args[i] === '-killswitch' && i + 1 < appData.args.length) {
          vpnClientData.killswitch = (appData.args[i + 1] as string).toLowerCase() === 'true';
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
        this.vpnSavedDataService.compareCurrentServer(vpnClientData.serverPk);

        if (vpnClientData.running) {
          this.lastState = VpnStates.Running;
        } else {
          this.lastState = VpnStates.Off;
        }

        this.currentEventData.running = vpnClientData.running;
        this.currentEventData.killswitch = vpnClientData.killswitch;
        this.currentEventData.appState = vpnClientData.appState;
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
}
