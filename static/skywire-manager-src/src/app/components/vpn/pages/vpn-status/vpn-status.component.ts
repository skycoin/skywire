import { Component, OnInit, OnDestroy } from '@angular/core';
import { Subscription } from 'rxjs';
import { ActivatedRoute } from '@angular/router';
import { MatDialog } from '@angular/material/dialog';

import { vpnTabsData } from '../../vpn-helpers';
import { VpnClientService } from 'src/app/services/vpn-client.service';
import GeneralUtils from 'src/app/utils/generalUtils';

@Component({
  selector: 'app-vpn-status',
  templateUrl: './vpn-status.component.html',
  styleUrls: ['./vpn-status.component.scss'],
})
export class VpnStatusComponent implements OnInit, OnDestroy {
  tabsData = vpnTabsData;

  receivedHistory: number[] = [20, 25, 40, 100, 35, 45, 45, 10, 20, 20];
  sentHistory: number[] = [30, 20, 40, 10, 35, 45, 45, 10, 20, 20];

  loading = true;
  showStarted = false;
  waitingResponse = false;
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
  ) {}

  ngOnInit() {
    this.navigationsSubscription = this.route.paramMap.subscribe(params => {
      if (params.has('key')) {
        this.vpnClientService.initialize(params.get('key'));
        this.currentLocalPk = params.get('key');
      }

      setTimeout(() => this.navigationsSubscription.unsubscribe());
    });

    this.dataSubscription = this.vpnClientService.backendState.subscribe(data => {
      if (data && data.vpnClient) {
        this.showStarted = data.vpnClient.running;

        this.currentRemotePk = data.vpnClient.serverPk;

        this.loading = false;
        this.waitingResponse = false;
      }
    });
  }

  ngOnDestroy() {
    this.dataSubscription.unsubscribe();
    this.navigationsSubscription.unsubscribe();
    this.closeOperationSubscription();
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

    this.vpnClientService.changeAppState(startApp).subscribe(() => {
      setTimeout(() => {
        this.vpnClientService.updateData();
      }, 500);
    });
  }

  private closeOperationSubscription() {
    if (this.operationSubscription) {
      this.operationSubscription.unsubscribe();
    }
  }
}
