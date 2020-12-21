import { Component, OnInit, OnDestroy } from '@angular/core';
import { Subscription } from 'rxjs';
import { ActivatedRoute } from '@angular/router';
import { MatDialog } from '@angular/material/dialog';

import { VpnHelpers } from '../../vpn-helpers';
import { AppState, BackendState, VpnClientService, VpnStates } from 'src/app/services/vpn-client.service';
import GeneralUtils from 'src/app/utils/generalUtils';
import { LocalServerData, VpnSavedDataService } from 'src/app/services/vpn-saved-data.service';
import { countriesList } from 'src/app/utils/countries-list';

@Component({
  selector: 'app-vpn-status',
  templateUrl: './vpn-status.component.html',
  styleUrls: ['./vpn-status.component.scss'],
})
export class VpnStatusComponent implements OnInit, OnDestroy {
  tabsData = VpnHelpers.vpnTabsData;

  receivedHistory: number[] = [20, 25, 40, 100, 35, 45, 45, 10, 20, 20];
  sentHistory: number[] = [30, 20, 40, 10, 35, 45, 45, 10, 20, 20];

  loading = true;
  showStarted = false;
  lastAppState: AppState = null;
  showBusy = false;
  waitingSteps = 0;
  currentIp: string;
  ipCountry: string;
  loadingCurrentIp = true;
  problemGettingIp = false;
  problemGettingIpCountry = false;

  currentLocalPk: string;
  currentRemoteServer: LocalServerData;
  backendState: BackendState;

  private dataSubscription: Subscription;
  private currentRemoteServerSubscription: Subscription;
  private operationSubscription: Subscription;
  private navigationsSubscription: Subscription;
  private ipSubscription: Subscription;

  constructor(
    private vpnClientService: VpnClientService,
    private vpnSavedDataService: VpnSavedDataService,
    private route: ActivatedRoute,
    private dialog: MatDialog,
  ) { }

  ngOnInit() {
    this.navigationsSubscription = this.route.paramMap.subscribe(params => {
      if (params.has('key')) {
        this.currentLocalPk = params.get('key');
        VpnHelpers.changeCurrentPk(this.currentLocalPk);
        this.tabsData = VpnHelpers.vpnTabsData;
      }

      setTimeout(() => this.navigationsSubscription.unsubscribe());

      this.dataSubscription = this.vpnClientService.backendState.subscribe(data => {
        if (data && data.serviceState !== VpnStates.PerformingInitialCheck) {
          this.backendState = data;

          if (this.lastAppState !== data.appState) {
            if (data.appState === AppState.Running || data.appState === AppState.Stopped) {
              this.getIp();
            }
          }

          this.lastAppState = data.appState;

          this.showStarted = data.running;
          this.showBusy = data.busy;

          this.loading = false;
        }
      });

      this.currentRemoteServerSubscription = this.vpnSavedDataService.currentServerObservable.subscribe(server => {
        this.currentRemoteServer = server;
      });
    });

    this.getIp();
  }

  ngOnDestroy() {
    this.dataSubscription.unsubscribe();
    this.navigationsSubscription.unsubscribe();
    this.currentRemoteServerSubscription.unsubscribe();
    this.ipSubscription.unsubscribe();
    this.closeOperationSubscription();
  }

  start() {
    this.showBusy = true;

    this.vpnClientService.start();
  }

  stop() {
    const confirmationDialog = GeneralUtils.createConfirmationDialog(this.dialog, 'vpn.status-page.disconnect-confirmation');

    confirmationDialog.componentInstance.operationAccepted.subscribe(() => {
      confirmationDialog.componentInstance.closeModal();

      this.showBusy = true;

      this.vpnClientService.stop();
    });
  }

  getCountryName(countryCode: string): string {
    return countriesList[countryCode.toUpperCase()] ? countriesList[countryCode.toUpperCase()] : countryCode;
  }

  get currentStateText(): string {
    if (this.backendState.appState === AppState.Stopped) {
      return 'vpn.connection-info.state-disconnected';
    } else if (this.backendState.appState === AppState.Connecting) {
      return 'vpn.connection-info.state-connecting';
    } else if (this.backendState.appState === AppState.Running) {
      return 'vpn.connection-info.state-connected';
    } else if (this.backendState.appState === AppState.ShuttingDown) {
      return 'vpn.connection-info.state-disconnecting';
    } else if (this.backendState.appState === AppState.Reconnecting) {
      return 'vpn.connection-info.state-reconnecting';
    }
  }

  private closeOperationSubscription() {
    if (this.operationSubscription) {
      this.operationSubscription.unsubscribe();
    }
  }

  private getIp() {
    if (this.ipSubscription) {
      this.ipSubscription.unsubscribe();
    }

    this.loadingCurrentIp = true;

    this.ipSubscription = this.vpnClientService.getIp().subscribe(response => {
      this.loadingCurrentIp = false;
      this.problemGettingIp = false;
      this.problemGettingIpCountry = false;

      if (response) {
        if (response.ip) {
          this.currentIp = response.ip;
        } else {
          this.problemGettingIp = true;
        }

        if (response.country) {
          this.ipCountry = response.country;
        } else {
          this.problemGettingIpCountry = true;
        }
      } else {
        this.problemGettingIp = true;
        this.problemGettingIpCountry = true;
      }
    }, ip => {
      this.loadingCurrentIp = false;
      this.problemGettingIpCountry = true;

      if (ip) {
        this.problemGettingIp = false;
        this.currentIp = ip;
      } else {
        this.problemGettingIp = true;
      }
    });
  }
}
