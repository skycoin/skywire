import { MatDialog, MatDialogRef } from '@angular/material/dialog';
import { Router } from '@angular/router';
import { map, mergeMap } from 'rxjs/operators';
import { Observable, of } from 'rxjs';

import { TabButtonData } from '../layout/top-bar/top-bar.component';
import { VpnClientService, CheckPkResults } from 'src/app/services/vpn-client.service';
import { SnackbarService } from 'src/app/services/snackbar.service';
import GeneralUtils from 'src/app/utils/generalUtils';
import { VpnServer } from 'src/app/services/vpn-client-discovery.service';
import { ManualVpnServerData } from './pages/vpn-server-list/add-vpn-server/add-vpn-server.component';
import { LocalServerData, ServerFlags, VpnSavedDataService } from 'src/app/services/vpn-saved-data.service';
import { Lists } from './pages/vpn-server-list/vpn-server-list.component';
import { SelectableOption, SelectOptionComponent } from '../layout/select-option/select-option.component';
import {
  EditVpnServerParams,
  EditVpnServerValueComponent
} from './pages/vpn-server-list/edit-vpn-server-value/edit-vpn-server-value.component';
import { EnterVpnServerPasswordComponent } from './pages/vpn-server-list/enter-vpn-server-password/enter-vpn-server-password.component';

/**
 * Helper functions for the VPN client.
 */
export class VpnHelpers {
  /**
   * Key for saving in sessionStorage the default tab that should be openned when entering to
   * the server list.
   */
  private static readonly serverListTabStorageKey = 'ServerListTab';

  /**
   * Pk of the local Skywire visor.
   */
  private static currentPk = '';
  /**
   * Sets the Pk of the local Skywire visor. Must be called for the vpnTabsData property to
   * work correctly.
   */
  static changeCurrentPk(pk: string): void {
    this.currentPk = pk;
  }

  /**
   * Allows to set the default tab that should be openned when entering to the server list.
   * It is saved while the tab is openned.
   */
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
   * Gets the string value to show in the UI a latency value, with an adecuate number of decimals.
   * This function converts the value from ms to segs, if appropriate, so the value must be shown
   * using the var returned by getLatencyValueString.
   */
  static getPrintableLatency(latency: number): string {
    if (latency < 1000) {
      return latency + '';
    }

    return (latency / 1000).toFixed(1);
  }

  /**
   * Changes the server and connects to it. One, and only one, of the newServer params must
   * be provided, with the data about the new server.
   */
  static processServerChange(
    router: Router,
    vpnClientService: VpnClientService,
    vpnSavedDataService: VpnSavedDataService,
    snackbarService: SnackbarService,
    dialog: MatDialog,
    dialogRef: MatDialogRef<any>,
    localPk: string,
    newServerFromHistory: LocalServerData,
    newServerFromDiscovery: VpnServer,
    newServerManually: ManualVpnServerData,
    password: string,
  ) {
    // Check if the new server param was provided as it should.
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

    // Check if the server was already saved and if the user could be changing the password.
    const savedServer = vpnSavedDataService.getSavedVersion(requestedPk, true);
    const passwordCouldHaveBeenChanged = savedServer && (password || savedServer.usedWithPassword);

    // Check if the selected server can be used.
    const result = vpnClientService.checkNewPk(requestedPk);

    // If the VPN service is busy, cancel the operation.
    if (result === CheckPkResults.Busy) {
      snackbarService.showError('vpn.server-change.busy-error');

      return;
    }

    // If the app is already connected to the selected server, cancel the operation, but not
    // if the password may have been changed.
    if (result === CheckPkResults.SamePkRunning && !passwordCouldHaveBeenChanged) {
      snackbarService.showWarning('vpn.server-change.already-selected-warning');

      return;
    }

    // If the app is connected to another server, ask for confirmation for stopping the
    // current connection before connecting with the new server. Also if the server was not
    // changed but the user could be changing the password.
    if (result === CheckPkResults.MustStop || (result === CheckPkResults.SamePkRunning && passwordCouldHaveBeenChanged)) {
      const confirmationDialog =
        GeneralUtils.createConfirmationDialog(dialog, 'vpn.server-change.change-server-while-connected-confirmation');

        confirmationDialog.componentInstance.operationAccepted.subscribe(() => {
          confirmationDialog.componentInstance.closeModal();

          if (newServerFromHistory) {
            vpnClientService.changeServerUsingHistory(newServerFromHistory, password);
          } else if (newServerFromDiscovery) {
            vpnClientService.changeServerUsingDiscovery(newServerFromDiscovery, password);
          } else if (newServerManually) {
            vpnClientService.changeServerManually(newServerManually, password);
          }

          VpnHelpers.redirectAfterServerChange(router, dialogRef, localPk);
        });

        return;
    }

    // If the server has already been selected, inform the user and continue after confirmation.
    // Not if the user could be changing the password, to allow to make the change.
    if (result === CheckPkResults.SamePkStopped && !passwordCouldHaveBeenChanged) {
      const confirmationDialog = GeneralUtils.createConfirmationDialog(dialog, 'vpn.server-change.start-same-server-confirmation');
      confirmationDialog.componentInstance.operationAccepted.subscribe(() => {
        confirmationDialog.componentInstance.closeModal();

        // Update the data in persistent storage, if it was entered manually.
        if (newServerManually && savedServer) {
          vpnSavedDataService.processFromManual(newServerManually);
        }

        vpnClientService.start();
        VpnHelpers.redirectAfterServerChange(router, dialogRef, localPk);
      });

      return;
    }

    // If none of the other conditions were met, change the server immediately.
    if (newServerFromHistory) {
      vpnClientService.changeServerUsingHistory(newServerFromHistory, password);
    } else if (newServerFromDiscovery) {
      vpnClientService.changeServerUsingDiscovery(newServerFromDiscovery, password);
    } else if (newServerManually) {
      vpnClientService.changeServerManually(newServerManually, password);
    }

    // Go to the status page.
    VpnHelpers.redirectAfterServerChange(router, dialogRef, localPk);
  }

  /**
   * Opens the status page. If a modal window ref is provided, it is closed.
   */
  static redirectAfterServerChange(
    router: Router,
    dialogRef: MatDialogRef<any>,
    localPk: string,
  ) {
    if (dialogRef) {
      dialogRef.close();
    }

    router.navigate(['vpn', localPk, 'status']);
  }

  /**
   * Opens the server options modal window and manages the interations the users may have with it.
   * @param server Sever for which the options will be openned.
   * @returns An observable for knowing if the user made a change.
   */
  static openServerOptions(
    server: LocalServerData,
    router: Router,
    vpnSavedDataService: VpnSavedDataService,
    vpnClientService: VpnClientService,
    snackbarService: SnackbarService,
    dialog: MatDialog
  ): Observable<boolean> {
    // List with the options that will be shown in the modal window.
    const options: SelectableOption[] = [];
    // List that, for each option added to the options array, will contain a code identifying
    // which operation the option is related to.
    const optionCodes: number[] = [];

    // Options for connecting with or without password.
    if (server.usedWithPassword) {
      options.push({ icon: 'lock_open', label: 'vpn.server-options.connect-without-password' });
      optionCodes.push(201);
    } else {
      // Allow to use a password only if the server was added manually.
      if (server.enteredManually) {
        options.push({ icon: 'lock_outlined', label: 'vpn.server-options.connect-using-password' });
        optionCodes.push(202);
      }
    }

    // Options for changing the custom name and personal note.
    options.push({ icon: 'edit', label: 'vpn.server-options.edit-name' });
    optionCodes.push(101);
    options.push({ icon: 'subject', label: 'vpn.server-options.edit-label' });
    optionCodes.push(102);

    // Option for adding the server to the favorites list.
    if (!server || server.flag !== ServerFlags.Favorite) {
      options.push({ icon: 'star', label: 'vpn.server-options.make-favorite' });
      optionCodes.push(1);
    }

    // Option for removing the server from the favorites list.
    if (server && server.flag === ServerFlags.Favorite) {
      options.push({ icon: 'star_outline', label: 'vpn.server-options.remove-from-favorites' });
      optionCodes.push(-1);
    }

    // Option for blocking the server.
    if (!server || server.flag !== ServerFlags.Blocked) {
      options.push({ icon: 'pan_tool', label: 'vpn.server-options.block' });
      optionCodes.push(2);
    }

    // Option for unblocking the server.
    if (server && server.flag === ServerFlags.Blocked) {
      options.push({ icon: 'thumb_up', label: 'vpn.server-options.unblock' });
      optionCodes.push(-2);
    }

    // Option for removing the server from the history.
    if (server && server.inHistory) {
      options.push({ icon: 'delete', label: 'vpn.server-options.remove-from-history'});
      optionCodes.push(-3);
    }

    // Show the options window.
    return SelectOptionComponent.openDialog(dialog, options, 'common.options').afterClosed().pipe(mergeMap((selectedOption: number) => {
      if (selectedOption) {
        // Get the saved version of the server, just in case it was modified in another tab.
        const updatedSavedVersion = vpnSavedDataService.getSavedVersion(server.pk, true);
        // Use the initially provided version, if there is no saved version.
        server = updatedSavedVersion ? updatedSavedVersion : server;

        selectedOption -= 1;

        if (optionCodes[selectedOption] > 200) {
          if (optionCodes[selectedOption] === 201) {
            let confirmed = false;

            // Ask for confirmation.
            const confirmationDialog = GeneralUtils.createConfirmationDialog(dialog, 'vpn.server-options.connect-without-password-confirmation');
            confirmationDialog.componentInstance.operationAccepted.subscribe(() => {
              confirmed = true;

              VpnHelpers.processServerChange(
                router,
                vpnClientService,
                vpnSavedDataService,
                snackbarService,
                dialog,
                null,
                VpnHelpers.currentPk,
                server,
                null,
                null,
                null,
              );

              confirmationDialog.componentInstance.closeModal();
            });

            // Return if the change was made.
            return confirmationDialog.afterClosed().pipe(map(() => confirmed));
          } else {
            return EnterVpnServerPasswordComponent.openDialog(dialog, false).afterClosed().pipe(map((password: string) => {
              // Continue only if the user did not cancel the operation.
              if (password && password !== '-') {
                VpnHelpers.processServerChange(
                  router,
                  vpnClientService,
                  vpnSavedDataService,
                  snackbarService,
                  dialog,
                  null,
                  VpnHelpers.currentPk,
                  server,
                  null,
                  null,
                  password.substr(1),
                );

                return true;
              }

              return false;
            }));
          }

          // Chage name or note.
        } else if (optionCodes[selectedOption] > 100) {
          const params: EditVpnServerParams = {
            editName: optionCodes[selectedOption] === 101,
            server: server
          };

          return EditVpnServerValueComponent.openDialog(dialog, params).afterClosed();

          // Make favorite.
        } else if (optionCodes[selectedOption] === 1) {
          return VpnHelpers.makeFavorite(server, vpnSavedDataService, snackbarService, dialog);

          // Remove from favorites.
        } else if (optionCodes[selectedOption] === -1) {
          vpnSavedDataService.changeFlag(server, ServerFlags.None);
          snackbarService.showDone('vpn.server-options.remove-from-favorites-done');

          return of(true);

          // Block.
        } else if (optionCodes[selectedOption] === 2) {
          return VpnHelpers.blockServer(server, vpnSavedDataService, vpnClientService, snackbarService, dialog);

          // Unblock.
        } else if (optionCodes[selectedOption] === -2) {
          vpnSavedDataService.changeFlag(server, ServerFlags.None);
          snackbarService.showDone('vpn.server-options.unblock-done');

          return of(true);

          // Remove from history.
        } else if (optionCodes[selectedOption] === -3) {
          return VpnHelpers.removeFromHistory(server, vpnSavedDataService, snackbarService, dialog);
        }
      }

      return of(false);
    }));
  }

  /**
   * Removes a server from history.
   * @returns An observable for knowing if the change was made.
   */
  private static removeFromHistory(
    server: LocalServerData,
    vpnSavedDataService: VpnSavedDataService,
    snackbarService: SnackbarService,
    dialog: MatDialog
  ): Observable<boolean> {
    let confirmed = false;

    // Ask for confirmation.
    const confirmationDialog = GeneralUtils.createConfirmationDialog(dialog, 'vpn.server-options.remove-from-history-confirmation');
    confirmationDialog.componentInstance.operationAccepted.subscribe(() => {
      confirmed = true;
      vpnSavedDataService.removeFromHistory(server.pk);
      snackbarService.showDone('vpn.server-options.remove-from-history-done');

      confirmationDialog.componentInstance.closeModal();
    });

    // Return if the change was made.
    return confirmationDialog.afterClosed().pipe(map(() => confirmed));
  }

  /**
   * Adds a server to the favorites list.
   * @returns An observable for knowing if the change was made.
   */
  private static makeFavorite(
    server: LocalServerData,
    vpnSavedDataService: VpnSavedDataService,
    snackbarService: SnackbarService,
    dialog: MatDialog
  ): Observable<boolean> {
    // If the server is not blocked, make the change immediately.
    if (server.flag !== ServerFlags.Blocked) {
      vpnSavedDataService.changeFlag(server, ServerFlags.Favorite);
      snackbarService.showDone('vpn.server-options.make-favorite-done');

      return of(true);
    }

    let confirmed = false;

    // If the server is blocked, ask for confirmation.
    const confirmationDialog = GeneralUtils.createConfirmationDialog(dialog, 'vpn.server-options.make-favorite-confirmation');
    confirmationDialog.componentInstance.operationAccepted.subscribe(() => {
      confirmed = true;
      vpnSavedDataService.changeFlag(server, ServerFlags.Favorite);
      snackbarService.showDone('vpn.server-options.make-favorite-done');

      confirmationDialog.componentInstance.closeModal();
    });

    return confirmationDialog.afterClosed().pipe(map(() => confirmed));
  }

  /**
   * Blocks a server.
   * @returns An observable for knowing if the change was made.
   */
  private static blockServer(
    server: LocalServerData,
    vpnSavedDataService: VpnSavedDataService,
    vpnClientService: VpnClientService,
    snackbarService: SnackbarService,
    dialog: MatDialog
  ): Observable<boolean> {
    // If the server is not in the favorites list and is not the currently selected server, make
    // the change immediately.
    if (server.flag !== ServerFlags.Favorite) {
      if (!vpnSavedDataService.currentServer || vpnSavedDataService.currentServer.pk !== server.pk) {
        vpnSavedDataService.changeFlag(server, ServerFlags.Blocked);
        snackbarService.showDone('vpn.server-options.block-done');

        return of(true);
      }
    }

    // Ask for confirmation.

    let confirmed = false;
    const mustStopVpn = vpnSavedDataService.currentServer && vpnSavedDataService.currentServer.pk === server.pk;

    let confirmationMsg: string;
    if (server.flag !== ServerFlags.Favorite) {
      // Msg for blocking the currently selected server.
      confirmationMsg = 'vpn.server-options.block-selected-confirmation';
    } else if (mustStopVpn) {
      // Msg for blocking the currently selected server if it is also in the favorites list.
      confirmationMsg = 'vpn.server-options.block-selected-favorite-confirmation';
    } else {
      // Msg for blocking a server from the favorites list.
      confirmationMsg = 'vpn.server-options.block-confirmation';
    }

    const confirmationDialog = GeneralUtils.createConfirmationDialog(dialog, confirmationMsg);
    confirmationDialog.componentInstance.operationAccepted.subscribe(() => {
      confirmed = true;
      vpnSavedDataService.changeFlag(server, ServerFlags.Blocked);
      snackbarService.showDone('vpn.server-options.block-done');

      // Stop the VPN if we are connected to the recently blocked server.
      if (mustStopVpn) {
        vpnClientService.stop();
      }

      confirmationDialog.componentInstance.closeModal();
    });

    return confirmationDialog.afterClosed().pipe(map(() => confirmed));
  }
}
