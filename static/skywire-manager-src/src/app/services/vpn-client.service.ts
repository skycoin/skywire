import { Injectable } from '@angular/core';
import { Observable, Subscription, of, BehaviorSubject } from 'rxjs';
import { mergeMap, delay } from 'rxjs/operators';

import { ApiService } from './api.service';

export class BackendState {
  errors: number;
  available: boolean;
  vpnClient: VpnClient;
}

export class VpnClient {
  running: boolean;
  serverPk: string;
}

@Injectable({
  providedIn: 'root'
})
export class VpnClientService {
  private readonly vpnClientAppName = 'vpn-client';

  private nodeKey: string;
  private stateSubject = new BehaviorSubject<BackendState>(null);
  private dataSubscription: Subscription;

  constructor(
    private apiService: ApiService,
  ) { }

  initialize(nodeKey: string) {
    if (!this.nodeKey) {
      this.nodeKey = nodeKey;

      this.continuallyUpdateData(0);
    } else {
      throw new Error('Already initialized');
    }
  }

  get backendState(): Observable<BackendState> {
    return this.stateSubject.asObservable();
  }

  updateData() {
    this.continuallyUpdateData(0);
  }

  changeAppState(startApp: boolean) {
    return this.apiService.put(`visors/${this.nodeKey}/apps/${encodeURIComponent(this.vpnClientAppName)}`,
      { status: startApp ? 1 : 0 }
    );
  }

  private continuallyUpdateData(delayMs: number) {
    if (this.dataSubscription) {
      this.dataSubscription.unsubscribe();
    }

    this.dataSubscription = of(0).pipe(
      delay(delayMs),
      mergeMap(() => this.apiService.get(`visors/${this.nodeKey}`))
    ).subscribe(nodeInfo => {
      let currentState = this.stateSubject.value;
      if (!currentState) {
        currentState = new BackendState();
        currentState.vpnClient = null;
      }
      currentState.available = false;
      currentState.errors = 0;

      if (nodeInfo && nodeInfo.apps && (nodeInfo.apps as any[]).length > 0) {
        let appData: any;
        (nodeInfo.apps as any[]).forEach(value => {
          if (value.name === this.vpnClientAppName) {
            appData = value;
          }
        });

        if (appData) {
          currentState.available = true;

          const vpnClientData = new VpnClient();
          vpnClientData.running = appData.status !== 0;

          if (appData.args && appData.args.length > 0) {
            for (let i = 0; i < appData.args.length; i++) {
              if (appData.args[i] === '-srv' && i + 1 < appData.args.length) {
                vpnClientData.serverPk = appData.args[i + 1];
              }
            }
          }

          currentState.vpnClient = vpnClientData;
        }
      }

      this.stateSubject.next(currentState);

      this.continuallyUpdateData(2000);
    }, () => {
      let currentState = this.stateSubject.value;
      if (!currentState) {
        currentState = new BackendState();
        currentState.vpnClient = null;
        currentState.errors = 0;
      }
      currentState.available = false;
      currentState.errors += 1;

      this.stateSubject.next(currentState);

      this.continuallyUpdateData(2000);
    });
  }
}
