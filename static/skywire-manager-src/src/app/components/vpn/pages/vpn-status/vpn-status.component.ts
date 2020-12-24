import { Component, OnInit, OnDestroy } from '@angular/core';
import { Subscription } from 'rxjs';
import { ActivatedRoute } from '@angular/router';
import { MatDialog } from '@angular/material/dialog';
import { TranslateService } from '@ngx-translate/core';

import { VpnHelpers } from '../../vpn-helpers';
import { AppState, BackendState, VpnClientService, VpnServiceStates } from 'src/app/services/vpn-client.service';
import GeneralUtils from 'src/app/utils/generalUtils';
import { LocalServerData, VpnSavedDataService } from 'src/app/services/vpn-saved-data.service';
import { countriesList } from 'src/app/utils/countries-list';
import { SnackbarService } from 'src/app/services/snackbar.service';

@Component({
  selector: 'app-vpn-status',
  templateUrl: './vpn-status.component.html',
  styleUrls: ['./vpn-status.component.scss'],
})
export class VpnStatusComponent implements OnInit, OnDestroy {
  tabsData = VpnHelpers.vpnTabsData;

  sentHistory: number[] = [0, 0, 0, 0, 0, 0, 0, 0, 0, 0];
  receivedHistory: number[] = [0, 0, 0, 0, 0, 0, 0, 0, 0, 0];
  uploadSpeed = 0;
  downloadSpeed = 0;
  totalUploaded = 0;
  totalDownloaded = 0;

  loading = true;
  showStartedLastValue = false;
  showStarted = false;
  lastAppState: AppState = null;
  showBusy = false;
  waitingSteps = 0;
  currentIp: string;
  previousIp: string;
  ipCountry: string;
  loadingCurrentIp = true;
  loadingIpCountry = true;
  problemGettingIp = false;
  problemGettingIpCountry = false;
  lastIpRefresDate = 0;

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
    private snackbarService: SnackbarService,
    private translateService: TranslateService,
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
        if (data && data.serviceState !== VpnServiceStates.PerformingInitialCheck) {
          this.backendState = data;

          if (this.lastAppState !== data.vpnClientAppData.appState) {
            if (data.vpnClientAppData.appState === AppState.Running || data.vpnClientAppData.appState === AppState.Stopped) {
              this.getIp(true);
            }
          }

          this.showStarted = data.vpnClientAppData.running;
          if (this.showStartedLastValue !== this.showStarted) {
            // Avoid replacing the whole var to prevent problems with the graph.
            for (let i = 0; i < 10; i++) {
              this.receivedHistory[i] = 0;
              this.sentHistory[i] = 0;
            }

            this.uploadSpeed = 0;
            this.downloadSpeed = 0;
            this.totalUploaded = 0;
            this.totalDownloaded = 0;
          }

          this.lastAppState = data.vpnClientAppData.appState;
          this.showStartedLastValue = this.showStarted;
          this.showBusy = data.busy;

          if (data.vpnClientAppData.connectionData) {
            // Avoid replacing the whole var to prevent problems with the graph.
            for (let i = 0; i < 10; i++) {
              this.receivedHistory[i] = data.vpnClientAppData.connectionData.downloadSpeedHistory[i];
              this.sentHistory[i] = data.vpnClientAppData.connectionData.uploadSpeedHistory[i];
            }

            this.uploadSpeed = data.vpnClientAppData.connectionData.uploadSpeed;
            this.downloadSpeed = data.vpnClientAppData.connectionData.downloadSpeed;
            this.totalUploaded = data.vpnClientAppData.connectionData.totalUploaded;
            this.totalDownloaded = data.vpnClientAppData.connectionData.totalDownloaded;
          }

          this.loading = false;
        }
      });

      this.currentRemoteServerSubscription = this.vpnSavedDataService.currentServerObservable.subscribe(server => {
        this.currentRemoteServer = server;
      });
    });

    this.getIp(true);
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
    if (this.backendState.vpnClientAppData.appState === AppState.Stopped) {
      return 'vpn.connection-info.state-disconnected';
    } else if (this.backendState.vpnClientAppData.appState === AppState.Connecting) {
      return 'vpn.connection-info.state-connecting';
    } else if (this.backendState.vpnClientAppData.appState === AppState.Running) {
      return 'vpn.connection-info.state-connected';
    } else if (this.backendState.vpnClientAppData.appState === AppState.ShuttingDown) {
      return 'vpn.connection-info.state-disconnecting';
    } else if (this.backendState.vpnClientAppData.appState === AppState.Reconnecting) {
      return 'vpn.connection-info.state-reconnecting';
    }
  }

  private closeOperationSubscription() {
    if (this.operationSubscription) {
      this.operationSubscription.unsubscribe();
    }
  }

  private getIp(ignoreTimeCheck = false) {
    if (!ignoreTimeCheck) {
      if (this.loadingCurrentIp || this.loadingIpCountry) {
        this.snackbarService.showWarning('vpn.status-page.data.ip-refresh-loading-warning');

        return;
      }

      const msToWait = 10000;
      if (Date.now() - this.lastIpRefresDate < msToWait) {
        const remainingSeconds = Math.ceil((msToWait - (Date.now() - this.lastIpRefresDate)) / 1000);

        this.snackbarService.showWarning(
          this.translateService.instant('vpn.status-page.data.ip-refresh-time-warning', {seconds: remainingSeconds})
        );

        return;
      }
    }

    if (this.ipSubscription) {
      this.ipSubscription.unsubscribe();
    }

    this.loadingCurrentIp = true;
    this.loadingIpCountry = true;

    this.previousIp = this.currentIp;

    this.ipSubscription = this.vpnClientService.getIp().subscribe(response => {
      this.loadingCurrentIp = false;
      this.lastIpRefresDate = Date.now();

      if (response) {
        this.problemGettingIp = false;
        this.currentIp = response;

        if (this.previousIp !== this.currentIp || this.problemGettingIpCountry) {
          this.getIpCountry();
        } else {
          this.loadingIpCountry = false;
        }
      } else {
        this.problemGettingIp = true;
        this.problemGettingIpCountry = true;
        this.loadingIpCountry = false;
      }
    }, () => {
      this.lastIpRefresDate = Date.now();
      this.loadingCurrentIp = false;
      this.loadingIpCountry = false;
      this.problemGettingIp = false;
      this.problemGettingIpCountry = true;
    });
  }

  private getIpCountry() {
    if (this.ipSubscription) {
      this.ipSubscription.unsubscribe();
    }

    this.loadingIpCountry = true;

    this.ipSubscription = this.vpnClientService.getIpCountry(this.currentIp).subscribe(response => {
      this.loadingIpCountry = false;

      this.lastIpRefresDate = Date.now();

      if (response) {
        this.problemGettingIpCountry = false;
        this.ipCountry = response;
      } else {
        this.problemGettingIpCountry = true;
      }
    }, () => {
      this.lastIpRefresDate = Date.now();
      this.loadingIpCountry = false;
      this.problemGettingIpCountry = true;
    });
  }
}
