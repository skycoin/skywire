import { Component } from '@angular/core';
import { Router, NavigationEnd } from '@angular/router';
import { MatDialog } from '@angular/material/dialog';
import { of, Subscription } from 'rxjs';
import { delay, mergeMap } from 'rxjs/operators';

import { StorageService } from './services/storage.service';
import { SnackbarService } from './services/snackbar.service';
import { LanguageService } from './services/language.service';
import { ApiService } from './services/api.service';
import { processServiceError } from './utils/errors';

/**
 * Root app component.
 */
@Component({
  selector: 'app-root',
  templateUrl: './app.component.html',
  styleUrls: ['./app.component.scss']
})
export class AppComponent {
  static currentInstance: AppComponent;

  // If the app is showing the VPN client.
  inVpnClient = false;
  // If the app is in the login page. Needed to know if content should be shown even if
  // hypervisorPkObtained is false.
  inLoginPage = false;

  // If the pk of the hypervisor has been obtained.
  hypervisorPkObtained = false;
  pkErrorShown = false;
  pkErrorsFound = 0;

  obtainPkSubscription: Subscription;

  constructor(
    // Imported to call its constructor right after opening the app.
    private storage: StorageService,
    router: Router,
    dialog: MatDialog,
    private snackbarService: SnackbarService,
    private languageService: LanguageService,
    private apiService: ApiService,
  ) {
    AppComponent.currentInstance = this;

    // Close the snackbar when opening a modal window.
    dialog.afterOpened.subscribe(() => snackbarService.closeCurrent());

    // Scroll to the top after navigating.
    // When navigating, scroll to the top and close the snackbar and all modal windows.
    router.events.subscribe(e => {
      if (e instanceof NavigationEnd) {
        snackbarService.closeCurrent();
        dialog.closeAll();
        window.scrollTo(0, 0);
      }
    });

    // After closing the modal windows, close the snackbar, but only if it is showing a temporary error,
    // as modal windows can open the snackbar for showing messages that should stay open.
    dialog.afterAllClosed.subscribe(() => snackbarService.closeCurrentIfTemporaryError());

    // Check if the app is showing the VPN client.
    router.events.subscribe((e: any) => {
      this.inVpnClient = router.url.includes('/vpn/') || router.url.includes('vpnlogin');

      // Check if the user enters or leaves the login page.
      if (e.url) {
        const previousInLoginPageValue = this.inLoginPage;
        this.inLoginPage = e.url.includes('login') || e.url.includes('tools/transports');

        if (previousInLoginPageValue && !this.inLoginPage && !this.hypervisorPkObtained) {
          this.checkHypervisorPk(0);
        }
      }

      // Show the correct document title.
      if (router.url.length > 2) {
        if (this.inVpnClient) {
          document.title = 'Skywire VPN';
        } else {
          document.title = 'Skywire Manager';
        }
      }
    });

    // Initialize the language configuration.
    this.languageService.loadLanguageSettings();

    this.checkHypervisorPk(0);
  }

  /**
   * This should be called a frame before leaving the login page, to avoid race conditions in which the
   * automatic event code in the constructor changes the value of inLoginPage to false but Angular still
   * loads the content of the new page just before taking that value into account.
   */
  processLoginDone() {
    this.inLoginPage = false;
    if (!this.hypervisorPkObtained) {
      this.checkHypervisorPk(0);
    }
  }

  /**
   * Gets the pk of the hypervisor. After that, it initializes services and allows the app to start working.
   */
  private checkHypervisorPk(delayMs: number) {
    if (this.obtainPkSubscription) {
      this.obtainPkSubscription.unsubscribe();
    }
    this.obtainPkSubscription = of(1).pipe(delay(delayMs), mergeMap(() => this.apiService.get('about'))).subscribe(result => {
      if (result.public_key) {
        this.finishStartup(result.public_key);
        this.hypervisorPkObtained = true;
      } else {
        if (!this.pkErrorShown) {
          this.snackbarService.showError('start.loading-error', null, true);
          this.pkErrorShown = true;
        }
        this.checkHypervisorPk(1000);
      }
    }, err => {
      this.pkErrorsFound += 1;

      if (this.pkErrorsFound > 4 && !this.pkErrorShown) {
        const e = processServiceError(err);
        this.snackbarService.showError('start.loading-error', null, true, e);
        this.pkErrorShown = true;
      }

      if (!this.inLoginPage) {
        this.checkHypervisorPk(1000);
      }
    });
  }

  private finishStartup(hypervisorPk: string) {
    this.storage.initialize(hypervisorPk);
  }
}
