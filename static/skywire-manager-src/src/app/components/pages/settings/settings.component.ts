import { Component } from '@angular/core';
import { Router } from '@angular/router';
import { MatDialog } from '@angular/material/dialog';

import { TabButtonData, MenuOptionData } from '../../layout/top-bar/top-bar.component';
import { AuthService } from '../../../services/auth.service';
import { SnackbarService } from '../../../services/snackbar.service';
import GeneralUtils from 'src/app/utils/generalUtils';

/**
 * Page with the general settings of the app.
 */
@Component({
  selector: 'app-settings',
  templateUrl: './settings.component.html',
  styleUrls: ['./settings.component.scss']
})
export class SettingsComponent {
  tabsData: TabButtonData[] = [];
  options: MenuOptionData[] = [];

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
}
