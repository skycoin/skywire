import { Component, OnInit, OnDestroy } from '@angular/core';
import { Subscription } from 'rxjs';
import { ActivatedRoute } from '@angular/router';
import { MatDialog } from '@angular/material/dialog';

import { VpnHelpers } from '../../vpn-helpers';
import { VpnClientService } from 'src/app/services/vpn-client.service';
import GeneralUtils from 'src/app/utils/generalUtils';
import { AppsService } from 'src/app/services/apps.service';

@Component({
  selector: 'app-vpn-status',
  templateUrl: './vpn-status.component.html',
  styleUrls: ['./vpn-status.component.scss'],
})
export class VpnStatusComponent implements OnInit, OnDestroy {
  public static requestedPk: string;
  public static requestedPassword: string;

  tabsData = VpnHelpers.vpnTabsData;

  receivedHistory: number[] = [20, 25, 40, 100, 35, 45, 45, 10, 20, 20];
  sentHistory: number[] = [30, 20, 40, 10, 35, 45, 45, 10, 20, 20];

  loading = true;
  showStarted = false;
  waitingResponse = false;
  configuringRequestedData = false;
  waitingSteps = 0;

  currentLocalPk: string;
  currentRemotePk: string;

  private dataSubscription: Subscription;
  private operationSubscription: Subscription;
  private navigationsSubscription: Subscription;

  constructor(
    private vpnClientService: VpnClientService,
    private route: ActivatedRoute,
    private dialog: MatDialog,
    private appsService: AppsService,
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
        if (data && data.vpnClient && !this.configuringRequestedData) {
          if (!VpnStatusComponent.requestedPk || data.vpnClient.serverPk.toUpperCase() === VpnStatusComponent.requestedPk.toUpperCase()) {
            this.showStarted = data.vpnClient.running;
            this.currentRemotePk = data.vpnClient.serverPk;

            VpnStatusComponent.requestedPk = null;

            this.waitingResponse = false;
          } else {
            this.showStarted = false;
            this.waitingResponse = true;
            this.configuringRequestedData = true;

            this.currentRemotePk = VpnStatusComponent.requestedPk;

            if (data.vpnClient.running) {
              this.changeAppState(false);
            } else {
              this.useRequestedPk();
            }
          }

          this.loading = false;
        }
      });
    });
  }

  ngOnDestroy() {
    this.dataSubscription.unsubscribe();
    this.navigationsSubscription.unsubscribe();
    this.closeOperationSubscription();

    VpnStatusComponent.requestedPk = null;
    VpnStatusComponent.requestedPassword = null;
  }

  start() {
    this.waitingResponse = true;

    this.changeAppState(true);
  }

  stop() {
    const confirmationDialog = GeneralUtils.createConfirmationDialog(this.dialog, 'vpn.status-page.disconnect-confirmation');

    confirmationDialog.componentInstance.operationAccepted.subscribe(() => {
      confirmationDialog.componentInstance.closeModal();

      this.waitingResponse = true;

      this.changeAppState(false);
    });
  }

  changeAppState(startApp: boolean) {
    this.closeOperationSubscription();

    // TODO: react in case of errors.
    this.operationSubscription = this.vpnClientService.changeAppState(startApp).subscribe(() => {
      setTimeout(() => {
        this.vpnClientService.updateData();

        if (this.configuringRequestedData) {
          if (!startApp) {
            this.useRequestedPk();
          } else {
            this.configuringRequestedData = false;
          }
        }
      }, 500);
    });
  }

  useRequestedPk() {
    this.closeOperationSubscription();

    const data = { pk: VpnStatusComponent.requestedPk };
    if (VpnStatusComponent.requestedPassword) {
      data['passcode'] = VpnStatusComponent.requestedPassword;
    } else {
      data['passcode'] = '';
    }

    // TODO: react in case of errors.
    this.operationSubscription = this.appsService.changeAppSettings(
      this.currentLocalPk,
      this.vpnClientService.vpnClientAppName,
      data,
    ).subscribe(
      () => {
        VpnStatusComponent.requestedPk = null;
        VpnStatusComponent.requestedPassword = null;

        this.changeAppState(true);
      }
    );
  }

  private closeOperationSubscription() {
    if (this.operationSubscription) {
      this.operationSubscription.unsubscribe();
    }
  }
}
