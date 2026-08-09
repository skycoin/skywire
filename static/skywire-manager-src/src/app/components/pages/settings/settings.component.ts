import { Component, OnDestroy, OnInit } from '@angular/core';
import { Router } from '@angular/router';
import { MatDialog } from '@angular/material/dialog';
import { Observable, Subscription, delay, mergeMap, of } from 'rxjs';

import { TabButtonData, MenuOptionData } from '../../layout/top-bar/top-bar.component';
import { homeTabsData } from 'src/app/utils/home-tabs';
import { AuthService, AuthStates } from '../../../services/auth.service';
import { StorageService } from '../../../services/storage.service';
import { SnackbarService } from '../../../services/snackbar.service';
import GeneralUtils from 'src/app/utils/generalUtils';
import { PageBaseComponent } from 'src/app/utils/page-base';

// skywireAutoUpdate is exposed by autoupdate.js (injected only by `cli hv serve`
// for the in-browser wasm-visor); absent for a native-hosted UI.
declare const window: any;

/**
 * Page with the general settings of the app.
 */
@Component({
    selector: 'app-settings',
    templateUrl: './settings.component.html',
    styleUrls: ['./settings.component.scss'],
    standalone: false
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
    private storageService: StorageService,
  ) {
    super();

    this.tabsData = homeTabsData();

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

  // --- wasm-visor auto-update (autoupdate.js / `cli hv serve`) ---------------

  /** Show the per-visor switcher as a full second row of tabs (opt-in) vs the
   *  compact dropdown default. */
  get showVisorSwitcherRow(): boolean {
    return this.storageService.getShowVisorSwitcherRow();
  }

  setShowVisorSwitcherRow(show: boolean): void {
    this.storageService.setShowVisorSwitcherRow(show);
  }

  /** True only when served as an in-browser wasm-visor with auto-update wired. */
  get autoUpdateAvailable(): boolean {
    return typeof window !== 'undefined' && !!window.skywireAutoUpdate;
  }

  get autoUpdateEnabled(): boolean {
    try {
      return !!window.skywireAutoUpdate && window.skywireAutoUpdate.isEnabled();
    } catch (e) {
      return false;
    }
  }

  setAutoUpdate(enabled: boolean) {
    if (!this.autoUpdateAvailable) {
      return;
    }
    if (enabled) {
      window.skywireAutoUpdate.enable();
      // Re-enabling: check immediately so a pending update isn't missed for a poll cycle.
      try {
 window.skywireAutoUpdate.checkNow(); 
} catch (e) {}
    } else {
      window.skywireAutoUpdate.disable();
    }
  }

  // --- wasm-visor fresh-worker reload (hv-boot.js window.skywireConfig) -------

  /**
   * True only when served as an in-browser wasm-visor, where hv-boot.js exposes
   * window.skywireConfig.reset() (terminate the SharedWorker + reload a fresh
   * blob). Absent for a native-hosted UI, so the action stays hidden there.
   */
  get reloadWorkerAvailable(): boolean {
    return typeof window !== 'undefined' && !!window.skywireConfig && typeof window.skywireConfig.reset === 'function';
  }

  reloadWorker() {
    if (!this.reloadWorkerAvailable) {
      return;
    }

    const confirmationDialog = GeneralUtils.createConfirmationDialog(this.dialog, 'settings.reload-worker.confirmation');

    confirmationDialog.componentInstance.operationAccepted.subscribe(() => {
      confirmationDialog.componentInstance.closeModal();
      try {
        window.skywireConfig.reset();
      } catch (e) {}
    });
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
