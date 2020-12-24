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
import { AppConfig } from '../app.config';
import { environment } from 'src/environments/environment';

export enum AppState {
  Stopped = 'stopped',
  Connecting = 'Connecting',
  Running = 'Running',
  ShuttingDown = 'Shutting down',
  Reconnecting = 'Connection failed, reconnecting',
}

export class BackendState {
  updateDate: number = Date.now();
  lastError: any;
  serviceState: VpnServiceStates;
  busy: boolean;
  vpnClientAppData: VpnClientAppData;
}

export class VpnClientAppData {
  running: boolean;
  serverPk: string;
  killswitch: boolean;
  appState: AppState;
  connectionData: VpnClientConnectionsData;
}

export class VpnClientConnectionsData {
  latency = 0;
  uploadSpeed = 0;
  downloadSpeed = 0;
  totalUploaded = 0;
  totalDownloaded = 0;
  downloadSpeedHistory: number[];
  uploadSpeedHistory: number[];
}

export enum VpnServiceStates {
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
  private lastState: VpnServiceStates;
  private working = true;

  private requestedServer: LocalServerData = null;
  private requestedPassword: string = null;

  private connectionHistoryPk: string;
  private downloadSpeedHistory: number[];
  private uploadSpeedHistory: number[];

  constructor(
    private apiService: ApiService,
    private appsService: AppsService,
    private router: Router,
    private vpnSavedDataService: VpnSavedDataService,
    private http: HttpClient,
  ) {
    this.currentEventData = new BackendState();
    this.currentEventData.busy = true;

    this.lastState = VpnServiceStates.PerformingInitialCheck;
  }

  initialize(nodeKey: string) {
    if (nodeKey) {
      if (!this.nodeKey) {
        this.nodeKey = nodeKey;

        this.vpnSavedDataService.initialize();

        this.continuallyUpdateData(0);
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

  getIp(): Observable<string> {
    if (!environment.production && AppConfig.vpn.hardcodedIpWhileDeveloping) {
      return of('8.8.8.8 ***');
    }

    return this.http.request('GET', 'https://api.ipify.org?format=json').pipe(
      retryWhen(errors => concat(errors.pipe(delay(2000), take(4)), throwError(''))),
      map(data => {
        if (data && data['ip']) {
          return  data['ip'];
        }

        return null;
      })
    );
  }

  getIpCountry(ip: string): Observable<string> {
    if (!environment.production && AppConfig.vpn.hardcodedIpWhileDeveloping) {
      return of('United States ***');
    }

    return this.http.request('GET', 'https://ip2c.org/' + ip, { responseType: 'text' }).pipe(
      retryWhen(errors => concat(errors.pipe(delay(2000), take(4)), throwError(''))),
      map(data => {
        let country: string;
        if (data) {
          const dataParts: string[] = data.split(';');

          if (dataParts.length === 4) {
            country = dataParts[3];
          }
        }

        return country;
      })
    );
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
    } else if (this.lastState !== VpnServiceStates.Off) {
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

        // TODO: the service could stay in working state.
      }
    );
  }

  private changeAppState(startApp: boolean) {
    if (this.working) {
      return;
    }

    const data = { status: 1 };

    if (startApp) {
      this.lastState = VpnServiceStates.Starting;
      this.connectionHistoryPk = null;
    } else {
      this.lastState = VpnServiceStates.Disconnecting;
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
        return this.getVpnClientState().pipe(mergeMap(appData => {
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
      retryWhen(errors => concat(errors.pipe(delay(this.standardWaitTime), take(10)), throwError('')))
    ).subscribe(response => {
      this.working = false;

      if (startApp) {
        if (this.currentEventData) {
          this.currentEventData.vpnClientAppData.running = true;
        }
        this.lastState = VpnServiceStates.Running;

        this.vpnSavedDataService.updateHistory();
      } else {
        if (this.currentEventData) {
          this.currentEventData.vpnClientAppData.running = false;
        }
        this.lastState = VpnServiceStates.Off;
      }
      this.sendUpdate();
      this.updateData();

      if (!startApp && this.requestedServer) {
        this.processServerChange();
      }
    }, err => {
      // TODO: the service could stay in working state.

      if (this.lastState === VpnServiceStates.Starting) {
        // TODO: process "Could not start".
      } else if (this.lastState === VpnServiceStates.Disconnecting) {
        // TODO: process Could not stop.
      } else {
        // TODO: process Should not happen.
      }
    });
  }

  private continuallyUpdateData(delayMs: number) {
    if (this.working && this.lastState !== VpnServiceStates.PerformingInitialCheck) {
      return;
    }

    if (this.continuousUpdateSubscription) {
      this.continuousUpdateSubscription.unsubscribe();
    }

    this.continuousUpdateSubscription = of(0).pipe(
      delay(delayMs),
      mergeMap(() => this.getVpnClientState()),
      retryWhen(errors => concat(
        errors.pipe(delay(this.standardWaitTime), take(this.lastState === VpnServiceStates.PerformingInitialCheck ? 5 : 1000000000)),
        throwError('')
      )),
    ).subscribe(appData => {
      if (appData) {
        if (this.lastState === VpnServiceStates.PerformingInitialCheck) {
          this.working = false;
        }

        this.vpnSavedDataService.compareCurrentServer(appData.serverPk);

        if (appData.running) {
          this.lastState = VpnServiceStates.Running;
        } else {
          this.lastState = VpnServiceStates.Off;
        }

        this.currentEventData.vpnClientAppData = appData;
        this.sendUpdate();
      } else if (this.lastState === VpnServiceStates.PerformingInitialCheck) {
        this.router.navigate(['vpn', 'unavailable']);
        this.nodeKey = null;
      }

      this.continuallyUpdateData(this.standardWaitTime);
    }, () => {
      this.router.navigate(['vpn', 'unavailable']);
      this.nodeKey = null;
    });
  }

  private stopContinuallyUpdateData() {
    this.working = true;

    if (this.continuousUpdateSubscription) {
      this.continuousUpdateSubscription.unsubscribe();
    }
  }

  private getVpnClientState(): Observable<VpnClientAppData> {
    let vpnClientData: VpnClientAppData;

    return this.apiService.get(`visors/${this.nodeKey}`).pipe(mergeMap(nodeInfo => {
      let appData: any;

      if (nodeInfo && nodeInfo.apps && (nodeInfo.apps as any[]).length > 0) {
        (nodeInfo.apps as any[]).forEach(value => {
          if (value.name === this.vpnClientAppName) {
            appData = value;
          }
        });
      }

      if (appData) {
        vpnClientData = new VpnClientAppData();
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
      }

      if (vpnClientData && vpnClientData.running) {
        return this.apiService.get(`visors/${this.nodeKey}/apps/${this.vpnClientAppName}/connections`);
      }

      return of(null);
    }), map((connectionsInfo: any[]) => {
      if (connectionsInfo && connectionsInfo.length > 0) {
        const vpnClientConnectionsData = new VpnClientConnectionsData();
        connectionsInfo.forEach(connection => {
          vpnClientConnectionsData.latency += connection.latency / connectionsInfo.length;
          vpnClientConnectionsData.uploadSpeed += connection.upload_speed / connectionsInfo.length;
          vpnClientConnectionsData.downloadSpeed += connection.download_speed / connectionsInfo.length;
          vpnClientConnectionsData.totalUploaded += connection.bandwidth_sent;
          vpnClientConnectionsData.totalDownloaded += connection.bandwidth_received;
        });

        if (!this.connectionHistoryPk || this.connectionHistoryPk !== vpnClientData.serverPk) {
          this.connectionHistoryPk = vpnClientData.serverPk;

          this.uploadSpeedHistory = [0, 0, 0, 0, 0, 0, 0, 0, 0, 0];
          this.downloadSpeedHistory = [0, 0, 0, 0, 0, 0, 0, 0, 0, 0];
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

        vpnClientData.connectionData = vpnClientConnectionsData;
      }

      return vpnClientData;
    }));
  }

  private sendUpdate() {
    this.currentEventData.serviceState = this.lastState;
    this.currentEventData.updateDate = Date.now();
    this.currentEventData.busy = this.working;
    this.stateSubject.next(this.currentEventData);
  }
}
