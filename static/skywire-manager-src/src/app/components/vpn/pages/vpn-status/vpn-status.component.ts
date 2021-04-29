import { Component, OnInit, OnDestroy } from '@angular/core';
import { Subscription } from 'rxjs';
import { ActivatedRoute, Router } from '@angular/router';
import { MatDialog } from '@angular/material/dialog';
import { TranslateService } from '@ngx-translate/core';
import BigNumber from 'bignumber.js';

import { VpnHelpers } from '../../vpn-helpers';
import { AppState, BackendState, VpnClientService, VpnServiceStates } from 'src/app/services/vpn-client.service';
import GeneralUtils from 'src/app/utils/generalUtils';
import { DataUnits, LocalServerData, ServerFlags, VpnSavedDataService } from 'src/app/services/vpn-saved-data.service';
import { countriesList } from 'src/app/utils/countries-list';
import { SnackbarService } from 'src/app/services/snackbar.service';
import { LineChartComponent } from 'src/app/components/layout/line-chart/line-chart.component';

/**
 * Page with the current state of the VPN. It also allows to start/stop the VPN protection.
 */
@Component({
  selector: 'app-vpn-status',
  templateUrl: './vpn-status.component.html',
  styleUrls: ['./vpn-status.component.scss'],
})
export class VpnStatusComponent implements OnInit, OnDestroy {
  // Data for populating the tabs of the top bar.
  tabsData = VpnHelpers.vpnTabsData;

  // Data for filling the data graphs while the VPN is working.
  sentHistory: number[] = [0, 0, 0, 0, 0, 0, 0, 0, 0, 0];
  receivedHistory: number[] = [0, 0, 0, 0, 0, 0, 0, 0, 0, 0];
  latencyHistory: number[] = [0, 0, 0, 0, 0, 0, 0, 0, 0, 0];
  minUploadInGraph = 0;
  midUploadInGraph = 0;
  maxUploadInGraph = 0;
  minDownloadInGraph = 0;
  midDownloadInGraph = 0;
  maxDownloadInGraph = 0;
  minLatencyInGraph = 0;
  midLatencyInGraph = 0;
  maxLatencyInGraph = 0;

  // Top margin of the data graphs.
  graphsTopInternalMargin = LineChartComponent.topInternalMargin;

  // Current data transmission stats.
  uploadSpeed = 0;
  downloadSpeed = 0;
  totalUploaded = 0;
  totalDownloaded = 0;
  latency = 0;

  showSpeedsInBits = true;
  showTotalsInBits = false;

  // If the state is being loaded for the first time.
  loading = true;
  // If the last time the state was obtained, it said that the VPN was running.
  showStartedLastValue = false;
  // If the VPN is currently running.
  showStarted = false;
  // State of the VPN client app the last time it was checked.
  lastAppState: AppState = null;
  // If the UI must be shown busy.
  showBusy = false;
  // If the user has not blocked the option for showing the IP info.
  ipInfoAllowed: boolean;
  // Public IP of the machine running the app.
  currentIp: string;
  // IP the machine running the app had the last time it was checked.
  previousIp: string;
  // Country of the public IP of the machine running the app.
  ipCountry: string;
  // If the current IP is being checked.
  loadingCurrentIp = true;
  // If the country of the current IP is being checked.
  loadingIpCountry = true;
  // If there was a problem the last time the code tried to get the current IP.
  problemGettingIp = false;
  // If there was a problem the last time the code tried to get the country of the current IP.
  problemGettingIpCountry = false;
  // Moment in which the IP was refreshed for the last time.
  private lastIpRefresDate = 0;
  // Pk of the local visor.
  currentLocalPk: string;
  // Currently selected server.
  currentRemoteServer: LocalServerData;
  // Extended data about the current state of the VPN client app.
  backendState: BackendState;

  serverFlags = ServerFlags;

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
    private router: Router,
  ) {
    this.ipInfoAllowed = this.vpnSavedDataService.getCheckIpSetting();

    // Set which units must be used for showing the data stats.
    const units: DataUnits = this.vpnSavedDataService.getDataUnitsSetting();
    if (units === DataUnits.OnlyBits) {
      this.showSpeedsInBits = true;
      this.showTotalsInBits = true;
    } else if (units === DataUnits.OnlyBytes) {
      this.showSpeedsInBits = false;
      this.showTotalsInBits = false;
    } else {
      this.showSpeedsInBits = true;
      this.showTotalsInBits = false;
    }
  }

  ngOnInit() {
    this.navigationsSubscription = this.route.paramMap.subscribe(params => {
      // Get the PK of the current local visor.
      if (params.has('key')) {
        this.currentLocalPk = params.get('key');
        VpnHelpers.changeCurrentPk(this.currentLocalPk);
        this.tabsData = VpnHelpers.vpnTabsData;
      }

      setTimeout(() => this.navigationsSubscription.unsubscribe());

      // Start getting and updating the state of the backend.
      this.dataSubscription = this.vpnClientService.backendState.subscribe(data => {
        if (data && data.serviceState !== VpnServiceStates.PerformingInitialCheck) {
          this.backendState = data;

          // If the state was changed, update the IP.
          if (this.lastAppState !== data.vpnClientAppData.appState) {
            if (data.vpnClientAppData.appState === AppState.Running || data.vpnClientAppData.appState === AppState.Stopped) {
              this.getIp(true);
            }
          }

          this.showStarted = data.vpnClientAppData.running;
          if (this.showStartedLastValue !== this.showStarted) {
            // If the running state changed, restart the values for the data graphs.

            // Avoid replacing the whole arrays to prevent problems with the graphs.
            for (let i = 0; i < 10; i++) {
              this.receivedHistory[i] = 0;
              this.sentHistory[i] = 0;
              this.latencyHistory[i] = 0;
            }
            this.updateGraphLimits();

            this.uploadSpeed = 0;
            this.downloadSpeed = 0;
            this.totalUploaded = 0;
            this.totalDownloaded = 0;
            this.latency = 0;
          }

          this.lastAppState = data.vpnClientAppData.appState;
          this.showStartedLastValue = this.showStarted;
          this.showBusy = data.busy;

          // Update the values for the data graphs.
          if (data.vpnClientAppData.connectionData) {
            // Avoid replacing the whole arrays to prevent problems with the graphs.
            for (let i = 0; i < 10; i++) {
              this.receivedHistory[i] = data.vpnClientAppData.connectionData.downloadSpeedHistory[i];
              this.sentHistory[i] = data.vpnClientAppData.connectionData.uploadSpeedHistory[i];
              this.latencyHistory[i] = data.vpnClientAppData.connectionData.latencyHistory[i];
            }

            this.updateGraphLimits();

            this.uploadSpeed = data.vpnClientAppData.connectionData.uploadSpeed;
            this.downloadSpeed = data.vpnClientAppData.connectionData.downloadSpeed;
            this.totalUploaded = data.vpnClientAppData.connectionData.totalUploaded;
            this.totalDownloaded = data.vpnClientAppData.connectionData.totalDownloaded;
            this.latency = data.vpnClientAppData.connectionData.latency;
          }

          this.loading = false;
        }
      });

      // Get or update the currently selected server.
      this.currentRemoteServerSubscription = this.vpnSavedDataService.currentServerObservable.subscribe(server => {
        this.currentRemoteServer = server;
      });
    });

    // Get the current IP.
    this.getIp(true);
  }

  ngOnDestroy() {
    this.dataSubscription.unsubscribe();
    this.navigationsSubscription.unsubscribe();
    this.currentRemoteServerSubscription.unsubscribe();
    this.closeOperationSubscription();

    if (this.ipSubscription) {
      this.ipSubscription.unsubscribe();
    }
  }

  /**
   * Starts the VPN protection.
   */
  start() {
    // If no server has been selected, open the server list.
    if (!this.currentRemoteServer) {
      this.router.navigate(['vpn', this.currentLocalPk, 'servers']);
      setTimeout(() => this.snackbarService.showWarning('vpn.status-page.select-server-warning'), 100);

      return;
    }

    // Cancel the operation if the server has been blocked.
    if (this.currentRemoteServer.flag === ServerFlags.Blocked) {
      this.snackbarService.showError('vpn.starting-blocked-server-error');

      return;
    }

    this.showBusy = true;

    this.vpnClientService.start();
  }

  /**
   * Start the process for stopping the VPN protection.
   */
  stop() {
    // If the killswitch option is not active, skip asking for confirmation.
    if (!this.backendState.vpnClientAppData.killswitch) {
      this.finishStoppingVpn();

      return;
    }

    // Ask for confirmation.
    const confirmationDialog = GeneralUtils.createConfirmationDialog(this.dialog, 'vpn.status-page.disconnect-confirmation');
    confirmationDialog.componentInstance.operationAccepted.subscribe(() => {
      confirmationDialog.componentInstance.closeModal();
      this.finishStoppingVpn();
    });
  }

  /**
   * Makes the actual request for stopping the VPN.
   */
  private finishStoppingVpn() {
    this.showBusy = true;
    this.vpnClientService.stop();
  }

  /**
   * Opens the options modal window for the currently selected server.
   */
  openServerOptions() {
    VpnHelpers.openServerOptions(
      this.currentRemoteServer,
      this.router,
      this.vpnSavedDataService,
      this.vpnClientService,
      this.snackbarService,
      this.dialog
    ).subscribe();
  }

  /**
   * Gets the full name of a country.
   * @param countryCode 2 letter code of the country.
   */
  getCountryName(countryCode: string): string {
    return countriesList[countryCode.toUpperCase()] ? countriesList[countryCode.toUpperCase()] : countryCode;
  }

  /**
   * Returns the translatable var that must be used for showing the notes of the currently
   * selected server. If there is only one note, the note itself is returned.
   */
  getNoteVar() {
    if (this.currentRemoteServer.note && this.currentRemoteServer.personalNote) {
      return 'vpn.server-list.notes-info';
    } else if (!this.currentRemoteServer.note && this.currentRemoteServer.personalNote) {
      return this.currentRemoteServer.personalNote;
    }

    return this.currentRemoteServer.note;
  }

  /**
   * Gets the name of the translatable var that must be used for showing a latency value. This
   * allows to add the correct measure suffix.
   */
  getLatencyValueString(latency: number): string {
    return VpnHelpers.getLatencyValueString(latency);
  }

  /**
   * Gets the string value to show in the UI a latency value with an adecuate number of decimals.
   * This function converts the value from ms to segs, if appropriate, so the value must be shown
   * using the var returned by getLatencyValueString.
   */
  getPrintableLatency(latency: number): string {
    return VpnHelpers.getPrintableLatency(latency);
  }

  /**
   * Translatable var that must be used for showing the current state of the VPN protection.
   */
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

  /**
   * Updates the vars with the limits the data graphs must show.
   */
  private updateGraphLimits() {
    const uploaded = this.calculateGraphLimits(this.sentHistory);
    this.minUploadInGraph = uploaded[0];
    this.midUploadInGraph = uploaded[1];
    this.maxUploadInGraph = uploaded[2];

    const downloaded = this.calculateGraphLimits(this.receivedHistory);
    this.minDownloadInGraph = downloaded[0];
    this.midDownloadInGraph = downloaded[1];
    this.maxDownloadInGraph = downloaded[2];

    const latency = this.calculateGraphLimits(this.latencyHistory);
    this.minLatencyInGraph = latency[0];
    this.midLatencyInGraph = latency[1];
    this.maxLatencyInGraph = latency[2];
  }

  /**
   * Calculates the limits a data graph must show.
   * @param arrayToCheck Array with the data that will be shown by the graph.
   * @returns An array with the min(0), mid(1) and max(2) values.
   */
  private calculateGraphLimits(arrayToCheck: number[]) {
    const min = 0;
    let max = 0;
    let mid = 0;

    arrayToCheck.forEach(val => {
      if (val > max) {
        max = val;
      }
    });

    // If the max and the min are the same, leave some spacing.
    if (min === max) {
      max += 1;
    }

    mid = (new BigNumber(max)).minus(min).dividedBy(2).plus(min).decimalPlaces(1).toNumber();

    return [min, mid, max];
  }

  /**
   * Checks and updates the public IP of the machine running the app and its country. The
   * operation is cancelled if the function was already called shortly before.
   * @param ignoreTimeCheck If true, the operation will be performed even if the function
   * was called shortly before.
   */
  private getIp(ignoreTimeCheck = false) {
    // Cancel the operation if the used blocked the IP checking functionality.
    if (!this.ipInfoAllowed) {
      return;
    }

    if (!ignoreTimeCheck) {
      // Cancel the operation if the IP or its country is already being obtained.
      if (this.loadingCurrentIp || this.loadingIpCountry) {
        this.snackbarService.showWarning('vpn.status-page.data.ip-refresh-loading-warning');

        return;
      }

      // Cancel the operation if the IP was updated shortly before.
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

    // Indicate that the IP and its country are being loaded.
    this.loadingCurrentIp = true;
    this.loadingIpCountry = true;

    this.previousIp = this.currentIp;

    // Get the IP.
    this.ipSubscription = this.vpnClientService.getIp().subscribe(response => {
      this.loadingCurrentIp = false;
      this.lastIpRefresDate = Date.now();

      if (response) {
        // Update the IP.
        this.problemGettingIp = false;
        this.currentIp = response;

        // Update the country, if the IP changed.
        if (this.previousIp !== this.currentIp || this.problemGettingIpCountry) {
          this.getIpCountry();
        } else {
          this.loadingIpCountry = false;
        }
      } else {
        // Indicate that there was a problem.
        this.problemGettingIp = true;
        this.problemGettingIpCountry = true;
        this.loadingIpCountry = false;
      }
    }, () => {
      // Indicate that there was a problem.
      this.lastIpRefresDate = Date.now();
      this.loadingCurrentIp = false;
      this.loadingIpCountry = false;
      this.problemGettingIp = false;
      this.problemGettingIpCountry = true;
    });
  }

  /**
   * Checks and updates the country of the public IP of the machine running the app. It was made
   * to be called from getIp().
   */
  private getIpCountry() {
    if (!this.ipInfoAllowed) {
      return;
    }

    if (this.ipSubscription) {
      this.ipSubscription.unsubscribe();
    }

    this.loadingIpCountry = true;

    // Get the country.
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
