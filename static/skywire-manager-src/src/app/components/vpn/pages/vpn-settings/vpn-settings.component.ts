import { Component, OnDestroy } from '@angular/core';
import { Subscription } from 'rxjs';
import { ActivatedRoute } from '@angular/router';

import { VpnHelpers } from '../../vpn-helpers';
import { BackendState, VpnClientService, VpnServiceStates } from 'src/app/services/vpn-client.service';
import { SnackbarService } from 'src/app/services/snackbar.service';
import { AppsService } from 'src/app/services/apps.service';
import { processServiceError } from 'src/app/utils/errors';
import { VpnSavedDataService } from 'src/app/services/vpn-saved-data.service';

enum WorkingOptions {
  None = 0,
  Killswitch = 1,
}

@Component({
  selector: 'app-vpn-settings-list',
  templateUrl: './vpn-settings.component.html',
  styleUrls: ['./vpn-settings.component.scss'],
})
export class VpnSettingsComponent implements OnDestroy {
  loading = true;
  backendData: BackendState;
  getIpOption: boolean;
  tabsData = VpnHelpers.vpnTabsData;

  currentLocalPk: string;

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
      if (params.has('key')) {
        this.currentLocalPk = params.get('key');
        VpnHelpers.changeCurrentPk(this.currentLocalPk);
        this.tabsData = VpnHelpers.vpnTabsData;
      }
    });

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

  getStatusClass(active: boolean): string {
    switch (active) {
      case true:
        return 'dot-green';
      default:
        return 'dot-red';
    }
  }

  getStatusText(active: boolean): string {
    switch (active) {
      case true:
        return 'vpn.settings-page.setting-on';
      default:
        return 'vpn.settings-page.setting-off';
    }
  }

  changeKillswitchOption() {
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

  changeGetIpOption() {
    this.getIpOption = !this.getIpOption;

    this.vpnSavedDataService.setCheckIpSetting(this.getIpOption);
  }
}
