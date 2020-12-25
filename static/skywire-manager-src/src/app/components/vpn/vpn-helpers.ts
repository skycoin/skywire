import { MatDialog, MatDialogRef } from '@angular/material/dialog';
import { Router } from '@angular/router';

import { TabButtonData } from '../layout/top-bar/top-bar.component';
import { VpnClientService, CheckPkResults } from 'src/app/services/vpn-client.service';
import { SnackbarService } from 'src/app/services/snackbar.service';
import GeneralUtils from 'src/app/utils/generalUtils';
import { VpnServer } from 'src/app/services/vpn-client-discovery.service';
import { ManualVpnServerData } from './pages/server-list/add-vpn-server/add-vpn-server.component';
import { LocalServerData } from 'src/app/services/vpn-saved-data.service';
import { Lists } from './pages/server-list/server-list.component';

export class VpnHelpers {
  private static readonly serverListTabStorageKey = 'ServerListTab';

  private static currentPk = '';

  static changeCurrentPk(pk: string): void {
    this.currentPk = pk;
  }

  static setDefaultTabForServerList(tab: Lists) {
    sessionStorage.setItem(VpnHelpers.serverListTabStorageKey, tab);
  }

  /**
   * Data for configuring the tab-bar shown in the header of the vpn client pages.
   */
  static get vpnTabsData(): TabButtonData[] {
    const lastServerListTab = sessionStorage.getItem(VpnHelpers.serverListTabStorageKey);

    return [
      {
        icon: 'power_settings_new',
        label: 'vpn.start',
        linkParts: ['/vpn', this.currentPk, 'status'],
      },
      {
        icon: 'list',
        label: 'vpn.servers',
        linkParts: lastServerListTab ? ['/vpn', this.currentPk, 'servers', lastServerListTab, '1'] : ['/vpn', this.currentPk, 'servers'],
      },
      {
        icon: 'settings',
        label: 'vpn.settings',
        linkParts: ['/vpn', this.currentPk, 'settings'],
      },
    ];
  }

  /**
   * Gets the name of the translatable var that must be used for showing a latency value. This
   * allows to add the correct measure suffix.
   */
  static getLatencyValueString(latency: number): string {
    if (latency < 1000) {
      return 'time-in-ms';
    }

    return 'time-in-segs';
  }

  /**
   * Gets the string value to show in the UI a latency value with an adecuate number of decimals.
   * This function converts the value from ms to segs, if appropriate, so the value must be shown
   * using the var returned by getLatencyValueString.
   */
  static getPrintableLatency(latency: number): string {
    if (latency < 1000) {
      return latency + '';
    }

    return (latency / 1000).toFixed(1);
  }

  static processServerChange(
    router: Router,
    vpnClientService: VpnClientService,
    snackbarService: SnackbarService,
    dialog: MatDialog,
    dialogRef: MatDialogRef<any>,
    localPk: string,
    newServerFromHistory: LocalServerData,
    newServerFromDiscovery: VpnServer,
    newServerManually: ManualVpnServerData,
  ) {
    let requestedPk: string;
    if ((newServerFromHistory && (newServerFromDiscovery || newServerManually)) ||
      (newServerFromDiscovery && (newServerFromHistory || newServerManually)) ||
      (newServerManually && (newServerFromHistory || newServerFromDiscovery))
    ) {
      throw new Error('Invalid call');
    }

    if (newServerFromHistory) {
      requestedPk = newServerFromHistory.pk;
    } else if (newServerFromDiscovery) {
      requestedPk = newServerFromDiscovery.pk;
    } else if (newServerManually) {
      requestedPk = newServerManually.pk;
    } else {
      throw new Error('Invalid call');
    }

    const result = vpnClientService.checkNewPk(requestedPk);

    if (result === CheckPkResults.Busy) {
      snackbarService.showError('vpn.server-change.busy-error');

      return;
    }

    if (result === CheckPkResults.SamePkRunning) {
      snackbarService.showWarning('vpn.server-change.already-selected-warning');

      return;
    }

    if (result === CheckPkResults.MustStop) {
      const confirmationDialog =
        GeneralUtils.createConfirmationDialog(dialog, 'vpn.server-change.change-server-while-connected-confirmation');

        confirmationDialog.componentInstance.operationAccepted.subscribe(() => {
          confirmationDialog.componentInstance.closeModal();

          if (newServerFromHistory) {
            vpnClientService.changeServerUsingHistory(newServerFromHistory);
          } else if (newServerFromDiscovery) {
            vpnClientService.changeServerUsingDiscovery(newServerFromDiscovery);
          } else if (newServerManually) {
            vpnClientService.changeServerManually(newServerManually);
          }

          VpnHelpers.redirectAfterServerChange(router, dialogRef, localPk);
        });

        return;
    }

    if (result === CheckPkResults.SamePkStopped) {
      const confirmationDialog =
        GeneralUtils.createConfirmationDialog(dialog, 'vpn.server-change.start-same-server-confirmation');

        confirmationDialog.componentInstance.operationAccepted.subscribe(() => {
          confirmationDialog.componentInstance.closeModal();

          vpnClientService.start();
          VpnHelpers.redirectAfterServerChange(router, dialogRef, localPk);
        });

        return;
    }

    if (newServerFromHistory) {
      vpnClientService.changeServerUsingHistory(newServerFromHistory);
    } else if (newServerFromDiscovery) {
      vpnClientService.changeServerUsingDiscovery(newServerFromDiscovery);
    } else if (newServerManually) {
      vpnClientService.changeServerManually(newServerManually);
    }

    VpnHelpers.redirectAfterServerChange(router, dialogRef, localPk);
  }

  private static redirectAfterServerChange(
    router: Router,
    dialogRef: MatDialogRef<any>,
    localPk: string,
  ) {
    if (dialogRef) {
      dialogRef.close();
    }

    router.navigate(['vpn', localPk, 'status']);
  }
}
