import { Component, OnDestroy } from '@angular/core';
import { Subscription } from 'rxjs';
import { ActivatedRoute } from '@angular/router';

import { VpnHelpers } from '../../vpn-helpers';
import { BackendState, VpnClientService, VpnServiceStates } from 'src/app/services/vpn-client.service';
import { SnackbarService } from 'src/app/services/snackbar.service';
import { AppsService } from 'src/app/services/apps.service';
import { processServiceError } from 'src/app/utils/errors';
import { VpnSavedDataService } from 'src/app/services/vpn-saved-data.service';

/**
 * Options that VpnSettingsComponent might be changing asynchronously.
 */
enum WorkingOptions {
  None = 0,
  Killswitch = 1,
}

/**
 * Page for changing the configuration of the VPN client.
 */
@Component({
  selector: 'app-vpn-settings-list',
  templateUrl: './vpn-settings.component.html',
  styleUrls: ['./vpn-settings.component.scss'],
})
export class VpnSettingsComponent implements OnDestroy {
  // If the data is being loaded.
  loading = true;
  // Current state of the VPN client app in the backend.
  backendData: BackendState;
  // If the option for getting the browser IP is active.
  getIpOption: boolean;
  // Data for populating the tabs of the top bar.
  tabsData = VpnHelpers.vpnTabsData;

  // Pk of the local visor.
  currentLocalPk: string;

  // Current option being changed asynchronously.
  working: WorkingOptions = WorkingOptions.None;
  workingOptions = WorkingOptions;

  private navigationsSubscription: Subscription;
  private dataSubscription: Subscription;
  private operationSubscription: Subscription;

  constructor(
    private vpnClientService: VpnClientService,
    private snackbarService: SnackbarService,
    private appsService: AppsService,
    private vpnSavedDataService: VpnSavedDataService,
    route: ActivatedRoute,
  ) {
    this.navigationsSubscription = route.paramMap.subscribe(params => {
      // Get the PK of the current local visor.
      if (params.has('key')) {
        this.currentLocalPk = params.get('key');
        VpnHelpers.changeCurrentPk(this.currentLocalPk);
        this.tabsData = VpnHelpers.vpnTabsData;
      }
    });

    // Get the current state of the VPN client app in the backend.
    this.dataSubscription = this.vpnClientService.backendState.subscribe(data => {
      if (data && data.serviceState !== VpnServiceStates.PerformingInitialCheck) {
        this.backendData = data;

        this.loading = false;
      }
    });

    this.getIpOption = this.vpnSavedDataService.getCheckIpSetting();
  }

  ngOnDestroy() {
    this.navigationsSubscription.unsubscribe();
    this.dataSubscription.unsubscribe();

    if (this.operationSubscription) {
      this.operationSubscription.unsubscribe();
    }
  }

  /**
   * Returns the css class that must be used for the dot indicating the current state of an option.
   * @param active If the option is active or not.
   */
  getStatusClass(active: boolean): string {
    switch (active) {
      case true:
        return 'dot-green';
      default:
        return 'dot-red';
    }
  }

  /**
   * Returns the translatable var that must be used for indicating the current state of an option.
   * @param active If the option is active or not.
   */
  getStatusText(active: boolean): string {
    switch (active) {
      case true:
        return 'vpn.settings-page.setting-on';
      default:
        return 'vpn.settings-page.setting-off';
    }
  }

  /**
   * Changes the killswitch option.
   */
  changeKillswitchOption() {
    // Do not continue if another option is being changed.
    if (this.working !== WorkingOptions.None) {
      this.snackbarService.showWarning('vpn.settings-page.working-warning');

      return;
    }

    this.working = WorkingOptions.Killswitch;

    this.operationSubscription = this.appsService.changeAppSettings(
      this.currentLocalPk,
      this.vpnClientService.vpnClientAppName,
      { killswitch: !this.backendData.vpnClientAppData.killswitch },
    ).subscribe(
      () => {
        this.working = WorkingOptions.None;
        this.vpnClientService.updateData();
      },
      err => {
        this.working = WorkingOptions.None;

        err = processServiceError(err);
        this.snackbarService.showError(err);
      },
    );
  }

  /**
   * Changes the option for getting the browser IP.
   */
  changeGetIpOption() {
    this.getIpOption = !this.getIpOption;

    this.vpnSavedDataService.setCheckIpSetting(this.getIpOption);
  }
}
