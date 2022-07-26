import { Component, OnDestroy, OnInit } from '@angular/core';
import { Router } from '@angular/router';
import { MatDialog } from '@angular/material/dialog';
import { of, Subscription } from 'rxjs';
import { delay, flatMap } from 'rxjs/operators';

import { TabButtonData, MenuOptionData } from '../../layout/top-bar/top-bar.component';
import { AuthService, AuthStates } from '../../../services/auth.service';
import { SnackbarService } from '../../../services/snackbar.service';
import GeneralUtils from 'src/app/utils/generalUtils';
import { UpdaterStorageKeys } from 'src/app/services/node.service';

/**
 * Page with the general settings of the app.
 */
@Component({
  selector: 'app-settings',
  templateUrl: './settings.component.html',
  styleUrls: ['./settings.component.scss']
})
export class SettingsComponent implements OnInit, OnDestroy {
  tabsData: TabButtonData[] = [];
  options: MenuOptionData[] = [];

  // If true, the animation telling the user that the auth settings are being checked isn't shown.
  waitBeforeShowingLoading = true;
  authChecked = false;
  // Removes the password settings if the auth option is not active in the back-end.
  authActive = false;

  private authSubscription: Subscription;

  // TODO: must be removed if the old updater is removed.
  //mustShowUpdaterSettings = !!localStorage.getItem(UpdaterStorageKeys.UseCustomSettings);

  constructor(
    private authService: AuthService,
    private router: Router,
    private snackbarService: SnackbarService,
    private dialog: MatDialog,
  ) {
    // Data for populating the tab bar.
    this.tabsData = [
      {
        icon: 'view_headline',
        label: 'nodes.title',
        linkParts: ['/nodes'],
      },
      {
        icon: 'language',
        label: 'nodes.dmsg-title',
        linkParts: ['/nodes', 'dmsg'],
      },
      {
        icon: 'settings',
        label: 'settings.title',
        linkParts: ['/settings'],
      }
    ];

    // Options for the menu shown in the top bar.
    this.options = [
      {
        name: 'common.logout',
        actionName: 'logout',
        icon: 'power_settings_new'
      }
    ];
  }

  ngOnInit() {
    setTimeout(() => {
      this.waitBeforeShowingLoading = false;
    }, 500);

    this.checkAuth(0);
  }

  /**
   * Checks if the auth options are active and the user is authenticated.
   */
  private checkAuth(delayMilliseconds: number) {
    this.authSubscription = of(1).pipe(
      // Wait the delay.
      delay(delayMilliseconds),
      // Load the data. The node pk is obtained from the currently openned node page.
      flatMap(() => this.authService.checkLogin())
    ).subscribe(
      result => {
        this.authChecked = true;
        this.authActive = result === AuthStates.Logged;
      },
      () => {
        // Retry after a small delay.
        this.checkAuth(15000);
      },
    );
  }

  ngOnDestroy() {
    this.authSubscription.unsubscribe();
  }

  /**
   * Called when an option form the top bar is selected.
   * @param actionName Name of the selected option, as defined in the this.options array.
   */
  performAction(actionName: string) {
    if (actionName === 'logout') {
      this.logout();
    }
  }

  logout() {
    const confirmationDialog = GeneralUtils.createConfirmationDialog(this.dialog, 'common.logout-confirmation');

    confirmationDialog.componentInstance.operationAccepted.subscribe(() => {
      confirmationDialog.componentInstance.closeModal();

      this.authService.logout().subscribe(
        () => this.router.navigate(['login']),
        () => this.snackbarService.showError('common.logout-error')
      );
    });
  }

  // TODO: must be removed, with the text, if the old updater is removed.
  /*
  // Opens the updater settings, if the user confirms the operation.
  showUpdaterSettings() {
    const confirmationDialog = GeneralUtils.createConfirmationDialog(this.dialog, 'settings.updater-config.open-confirmation');

    confirmationDialog.componentInstance.operationAccepted.subscribe(() => {
      confirmationDialog.close();

      this.mustShowUpdaterSettings = true;
    });
  }
  */
}
