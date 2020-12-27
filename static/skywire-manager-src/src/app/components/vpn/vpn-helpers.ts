import { MatDialog, MatDialogRef } from '@angular/material/dialog';
import { Router } from '@angular/router';
import { map, mergeMap } from 'rxjs/operators';
import { Observable, of } from 'rxjs';

import { TabButtonData } from '../layout/top-bar/top-bar.component';
import { VpnClientService, CheckPkResults } from 'src/app/services/vpn-client.service';
import { SnackbarService } from 'src/app/services/snackbar.service';
import GeneralUtils from 'src/app/utils/generalUtils';
import { VpnServer } from 'src/app/services/vpn-client-discovery.service';
import { ManualVpnServerData } from './pages/server-list/add-vpn-server/add-vpn-server.component';
import { LocalServerData, ServerFlags, VpnSavedDataService } from 'src/app/services/vpn-saved-data.service';
import { Lists, VpnServerForList } from './pages/server-list/server-list.component';
import { SelectableOption, SelectOptionComponent } from '../layout/select-option/select-option.component';
import {
  EditVpnServerParams,
  EditVpnServerValueComponent
} from './pages/server-list/edit-vpn-server-value/edit-vpn-server-value.component';

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

          // For updating the data in persistent storage.
          if (newServerManually) {
            vpnClientService.changeServerManually(newServerManually);
          }

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

  static openServerOptions(
    server: LocalServerData,
    vpnSavedDataService: VpnSavedDataService,
    vpnClientService: VpnClientService,
    snackbarService: SnackbarService,
    dialog: MatDialog
  ) {
    const options: SelectableOption[] = [];
    const optionCodes: number[] = [];

    options.push({ icon: 'edit', label: 'vpn.server-options.edit-value.name-title' });
    optionCodes.push(101);
    options.push({ icon: 'subject', label: 'vpn.server-options.edit-value.note-title' });
    optionCodes.push(102);

    if (!server || server.flag !== ServerFlags.Favorite) {
      options.push({ icon: 'star', label: 'vpn.server-options.make-favorite' });
      optionCodes.push(1);
    }

    if (server && server.flag === ServerFlags.Favorite) {
      options.push({ icon: 'star_outline', label: 'vpn.server-options.remove-from-favorites' });
      optionCodes.push(-1);
    }

    if (!server || server.flag !== ServerFlags.Blocked) {
      options.push({ icon: 'pan_tool', label: 'vpn.server-options.block' });
      optionCodes.push(2);
    }

    if (server && server.flag === ServerFlags.Blocked) {
      options.push({ icon: 'thumb_up', label: 'vpn.server-options.unblock' });
      optionCodes.push(-2);
    }

    if (server && server.inHistory) {
      options.push({ icon: 'delete', label: 'vpn.server-options.remove-from-history'});
      optionCodes.push(-3);
    }

    return SelectOptionComponent.openDialog(dialog, options, 'common.options').afterClosed().pipe(mergeMap((selectedOption: number) => {
      if (selectedOption) {
        const updatedSavedVersion = vpnSavedDataService.getSavedVersion(server.pk, true);
        server = updatedSavedVersion ? updatedSavedVersion : server;

        selectedOption -= 1;

        if (optionCodes[selectedOption] > 100) {
          const params: EditVpnServerParams = {
            editName: optionCodes[selectedOption] === 101,
            server: server
          };

          return EditVpnServerValueComponent.openDialog(dialog, params).afterClosed();
        } else if (optionCodes[selectedOption] === 1) {
          return VpnHelpers.makeFavorite(server, vpnSavedDataService, snackbarService, dialog);
        } else if (optionCodes[selectedOption] === -1) {
          vpnSavedDataService.changeFlag(server, ServerFlags.None);
          snackbarService.showDone('vpn.server-options.remove-from-favorites-done');

          return of(true);
        } else if (optionCodes[selectedOption] === 2) {
          return VpnHelpers.blockServer(server, vpnSavedDataService, vpnClientService, snackbarService, dialog);
        } else if (optionCodes[selectedOption] === -2) {
          vpnSavedDataService.changeFlag(server, ServerFlags.None);
          snackbarService.showDone('vpn.server-options.unblock-done');

          return of(true);
        } else if (optionCodes[selectedOption] === -3) {
          return VpnHelpers.removeFromHistory(server, vpnSavedDataService, snackbarService, dialog);
        }
      }

      return of(false);
    }));
  }

  private static removeFromHistory(
    server: LocalServerData,
    vpnSavedDataService: VpnSavedDataService,
    snackbarService: SnackbarService,
    dialog: MatDialog
  ): Observable<boolean> {
    let confirmed = false;

    const confirmationDialog = GeneralUtils.createConfirmationDialog(dialog, 'vpn.server-options.remove-from-history-confirmation');
    confirmationDialog.componentInstance.operationAccepted.subscribe(() => {
      confirmed = true;
      vpnSavedDataService.removeFromHistory(server.pk);
      snackbarService.showDone('vpn.server-options.remove-from-history-done');

      confirmationDialog.componentInstance.closeModal();
    });

    return confirmationDialog.afterClosed().pipe(map(() => confirmed));
  }

  private static makeFavorite(
    server: LocalServerData,
    vpnSavedDataService: VpnSavedDataService,
    snackbarService: SnackbarService,
    dialog: MatDialog
  ): Observable<boolean> {
    if (server.flag !== ServerFlags.Blocked) {
      vpnSavedDataService.changeFlag(server, ServerFlags.Favorite);
      snackbarService.showDone('vpn.server-options.make-favorite-done');

      return of(true);
    }

    let confirmed = false;

    const confirmationDialog = GeneralUtils.createConfirmationDialog(dialog, 'vpn.server-options.make-favorite-confirmation');
    confirmationDialog.componentInstance.operationAccepted.subscribe(() => {
      confirmed = true;
      vpnSavedDataService.changeFlag(server, ServerFlags.Favorite);
      snackbarService.showDone('vpn.server-options.make-favorite-done');

      confirmationDialog.componentInstance.closeModal();
    });

    return confirmationDialog.afterClosed().pipe(map(() => confirmed));
  }

  private static blockServer(
    server: LocalServerData,
    vpnSavedDataService: VpnSavedDataService,
    vpnClientService: VpnClientService,
    snackbarService: SnackbarService,
    dialog: MatDialog
  ): Observable<boolean> {
    if (server.flag !== ServerFlags.Favorite) {
      if (!vpnSavedDataService.currentServer || vpnSavedDataService.currentServer.pk !== server.pk) {
        vpnSavedDataService.changeFlag(server, ServerFlags.Blocked);
        snackbarService.showDone('vpn.server-options.block-done');

        return of(true);
      }
    }

    let confirmed = false;
    const mustStopVpn = vpnSavedDataService.currentServer && vpnSavedDataService.currentServer.pk === server.pk;

    let confirmationMsg: string;
    if (server.flag !== ServerFlags.Favorite) {
      confirmationMsg = 'vpn.server-options.block-selected-confirmation';
    } else if (mustStopVpn) {
      confirmationMsg = 'vpn.server-options.block-selected-favorite-confirmation';
    } else {
      confirmationMsg = 'vpn.server-options.block-confirmation';
    }

    const confirmationDialog = GeneralUtils.createConfirmationDialog(dialog, confirmationMsg);
    confirmationDialog.componentInstance.operationAccepted.subscribe(() => {
      confirmed = true;
      vpnSavedDataService.changeFlag(server, ServerFlags.Blocked);
      snackbarService.showDone('vpn.server-options.block-done');

      if (mustStopVpn) {
        vpnClientService.stop();
      }

      confirmationDialog.componentInstance.closeModal();
    });

    return confirmationDialog.afterClosed().pipe(map(() => confirmed));
  }
}
