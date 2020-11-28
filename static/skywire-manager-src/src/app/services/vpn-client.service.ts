import { Injectable } from '@angular/core';
import { Observable, Subscription, of, BehaviorSubject, concat, throwError } from 'rxjs';
import { mergeMap, delay, retryWhen, take, catchError } from 'rxjs/operators';

import { ApiService } from './api.service';
import { AppsService } from './apps.service';

export class BackendState {
  lastError: any;
  available: boolean;
  vpnClient: VpnClient;
  serviceState: VpnStates;
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

@Injectable({
  providedIn: 'root'
})
export class VpnClientService {
  readonly vpnClientAppName = 'vpn-client';

  private requestedPk: string = null;
  private requestedPassword: string = null;

  private nodeKey: string;
  private stateSubject = new BehaviorSubject<BackendState>(null);
  private dataSubscription: Subscription;
  private continuousUpdateSubscription: Subscription;

  private currentEventData: BackendState;
  private lastState: VpnStates;
  private working = true;

  constructor(
    private apiService: ApiService,
    private appsService: AppsService,
  ) {
    this.currentEventData = new BackendState();
    this.currentEventData.vpnClient = null;
    this.currentEventData.available = true;

    this.lastState = VpnStates.PerformingInitialCheck;
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

  changeServer(pk: string, password: string): boolean {
    if (!this.working) {
      this.requestedPk = pk;
      this.requestedPassword = password;

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
      if (newPk === this.currentEventData.vpnClient.serverPk) {
        return CheckPkResults.SamePkRunning;
      } else {
        return CheckPkResults.MustStop;
      }
    } else if (newPk === this.currentEventData.vpnClient.serverPk) {
      return CheckPkResults.SamePkStopped;
    }

    return CheckPkResults.Ok;
  }

  private processServerChange() {
    if (this.dataSubscription) {
      this.dataSubscription.unsubscribe();
    }

    const data = { pk: this.requestedPk };
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
        this.requestedPk = null;
        this.requestedPassword = null;
        this.working = false;

        if (this.currentEventData && this.currentEventData.vpnClient) {
          this.currentEventData.vpnClient.serverPk = data.pk;
        }

        this.start();
      }, () => {
        // More processing needed.
        this.requestedPk = null;
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
      retryWhen(errors => concat(errors.pipe(delay(2000), take(10)), throwError('')))
    ).subscribe(response => {
      this.working = false;

      if (startApp) {
        if (this.currentEventData && this.currentEventData.vpnClient) {
          this.currentEventData.vpnClient.running = true;
        }
        this.lastState = VpnStates.Running;
      } else {
        if (this.currentEventData && this.currentEventData.vpnClient) {
          this.currentEventData.vpnClient.running = false;
        }
        this.lastState = VpnStates.Off;
      }
      this.sendUpdate();
      this.updateData();

      if (!startApp && this.requestedPk) {
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
      retryWhen(errors => concat(errors.pipe(delay(2000), take(10)), throwError('')))
    ).subscribe(nodeInfo => {
      const appData = this.extractVpnAppData(nodeInfo);
      if (appData) {
        this.working = false;

        const vpnClientData = this.getVpnClientData(appData);

        if (vpnClientData.running) {
          this.lastState = VpnStates.Running;
        } else {
          this.lastState = VpnStates.Off;
        }

        this.currentEventData.vpnClient = vpnClientData;
        this.sendUpdate();

        this.continuallyUpdateData(2000);
      } else {
        this.currentEventData.available = false;
        this.currentEventData.lastError = 'vpn.unavailable-error';
        this.sendUpdate();
      }
    }, err => {
      this.currentEventData.available = false;
      this.currentEventData.lastError = err;
      this.sendUpdate();
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
      retryWhen(errors => errors.pipe(delay(2000)))
    ).subscribe(nodeInfo => {
      const appData = this.extractVpnAppData(nodeInfo);
      if (appData) {
        const vpnClientData = this.getVpnClientData(appData);

        if (vpnClientData.running) {
          this.lastState = VpnStates.Running;
        } else {
          this.lastState = VpnStates.Off;
        }

        this.currentEventData.vpnClient = vpnClientData;
        this.sendUpdate();
      }

      this.continuallyUpdateData(2000);
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
    this.stateSubject.next(this.currentEventData);
  }
}
