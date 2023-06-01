import { Component, OnDestroy, OnInit } from '@angular/core';
import { Router } from '@angular/router';
import { MatDialog } from '@angular/material/dialog';
import { Observable, of, Subscription } from 'rxjs';
import { delay, mergeMap } from 'rxjs/operators';

import { TabButtonData, MenuOptionData } from '../../layout/top-bar/top-bar.component';
import { AuthService, AuthStates } from '../../../services/auth.service';
import { SnackbarService } from '../../../services/snackbar.service';
import GeneralUtils from 'src/app/utils/generalUtils';
import { PageBaseComponent } from 'src/app/utils/page-base';

/**
 * Page with the general settings of the app.
 */
@Component({
  selector: 'app-settings',
  templateUrl: './settings.component.html',
  styleUrls: ['./settings.component.scss']
})
export class SettingsComponent extends PageBaseComponent implements OnInit, OnDestroy {
  // Keys for persisting the server data, to be able to restore the state after navigation.
  private readonly persistentAuthDataResponseKey = 'serv-aut-response';

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
    super();

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

    // Configure the options menu shown in the top bar.
    this.updateOptionsMenu();
  }

  ngOnInit() {
    setTimeout(() => {
      this.waitBeforeShowingLoading = false;
    }, 500);

    this.checkAuth(0, true);

    return super.ngOnInit();
  }

  /**
   * Checks if the auth options are active and the user is authenticated.
   */
  private checkAuth(delayMilliseconds: number, checkSavedData: boolean) {
    // Use saved data or get from the server. If there is no saved data, savedData is null.
    const savedData = checkSavedData ? this.getLocalValue(this.persistentAuthDataResponseKey) : null;
    let nextOperation: Observable<any> = this.authService.checkLogin();
    if (savedData) {
      nextOperation = of(JSON.parse(savedData.value));
    }

    this.authSubscription = of(1).pipe(
      // Wait the delay.
      delay(delayMilliseconds),
      // Load the data. The node pk is obtained from the currently openned node page.
      mergeMap(() => nextOperation)
    ).subscribe(
      result => {
        if (!savedData) {
          this.saveLocalValue(this.persistentAuthDataResponseKey, JSON.stringify(result));
        }

        this.authChecked = true;
        this.authActive = result === AuthStates.Logged;

        this.updateOptionsMenu();

        // If old saved data was used, repeat the operation, ignoring the saved data.
        if (savedData) {
          this.checkAuth(0, false);
        }
      },
      () => {
        // Retry after a small delay.
        this.checkAuth(15000, false);
      },
    );
  }

  ngOnDestroy() {
    this.authSubscription.unsubscribe();
  }

  /**
   * Configures the options menu shown in the top bar.
   */
  private updateOptionsMenu() {
    this.options = [];

    if (this.authActive) {
      this.options = [
        {
          name: 'common.logout',
          actionName: 'logout',
          icon: 'power_settings_new'
        }
      ];
    }
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
