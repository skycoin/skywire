import { Injectable } from '@angular/core';
import { Observable, Subscription, of, BehaviorSubject, concat, throwError } from 'rxjs';
import { mergeMap, delay, retryWhen, take, catchError } from 'rxjs/operators';

import { ApiService } from './api.service';

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

@Injectable({
  providedIn: 'root'
})
export class VpnClientService {
  readonly vpnClientAppName = 'vpn-client';

  private nodeKey: string;
  private stateSubject = new BehaviorSubject<BackendState>(null);
  private dataSubscription: Subscription;
  private continuousUpdateSubscription: Subscription;

  private currentEventData: BackendState;
  private lastState: VpnStates;
  private working = true;

  constructor(
    private apiService: ApiService,
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

  start() {
    if (!this.working && this.lastState < 20) {
      this.currentEventData.lastError = null;

      this.changeAppState(true);
    }
  }

  stop() {
    if (!this.working && this.lastState >= 20 && this.lastState < 200) {
      this.changeAppState(false);
    }
  }

  private changeAppState(startApp: boolean) {
    if (startApp) {
      this.lastState = VpnStates.Starting;
    } else {
      this.lastState = VpnStates.Disconnecting;
    }

    if (this.dataSubscription) {
      this.dataSubscription.unsubscribe();
    }

    this.stopContinuallyUpdateData();
    this.sendUpdate();

    this.dataSubscription = this.apiService.put(
      `visors/${this.nodeKey}/apps/${encodeURIComponent(this.vpnClientAppName)}`,
      { status: startApp ? 1 : 0 }
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
      this.updateData();
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
