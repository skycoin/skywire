import { Component, AfterViewInit, OnDestroy, Input } from '@angular/core';
import { MatDialog, MatDialogConfig } from '@angular/material/dialog';
import { Router } from '@angular/router';
import { Subscription } from 'rxjs';

import { ConfigurationComponent } from './configuration/configuration.component';
import { BasicTerminalComponent } from './basic-terminal/basic-terminal.component';
import { SnackbarService } from '../../../../services/snackbar.service';
import { NodeComponent } from '../node.component';
import { SidenavService } from 'src/app/services/sidenav.service';
import { Node } from '../../../../app.datatypes';
import GeneralUtils from 'src/app/utils/generalUtils';
import { NodeService } from 'src/app/services/node.service';
import { OperationError } from 'src/app/utils/operation-error';
import { processServiceError } from 'src/app/utils/errors';
import { SelectableOption, SelectOptionComponent } from 'src/app/components/layout/select-option/select-option.component';
import { ConfirmationData, ConfirmationComponent } from 'src/app/components/layout/confirmation/confirmation.component';
import { AppConfig } from 'src/app/app.config';
import { TranslateService } from '@ngx-translate/core';

/**
 * Component for making the options of the left bar of the nodes page to appear. It does not
 * have its own UI, it just works with SidenavService to make the options appear and work.
 */
@Component({
  selector: 'app-actions',
  templateUrl: './actions.component.html',
  styleUrls: ['./actions.component.scss']
})
export class ActionsComponent implements AfterViewInit, OnDestroy {
  /**
   * Allows to know if the currently displayed subpage is one dedicated to show a full list
   * of elements (true) or if it is one dedicated only to show a sumary (false).
   */
  @Input() set showingFullList(val: boolean) {
    this.showingFullListInternal = val;
    this.updateMenu();
  }
  private showingFullListInternal: boolean;

  private currentNode: Node;

  private menuSubscription: Subscription;
  private nodeSubscription: Subscription;
  private rebootSubscription: Subscription;
  private updateSubscription: Subscription;

  constructor(
    private dialog: MatDialog,
    private router: Router,
    private snackbarService: SnackbarService,
    private sidenavService: SidenavService,
    private nodeService: NodeService,
    private translateService: TranslateService,
  ) { }

  ngAfterViewInit() {
    this.nodeSubscription = NodeComponent.currentNode.subscribe((node: Node) => {
      this.currentNode = node;
    });

    this.updateMenu();
  }

  updateMenu() {
    setTimeout(() => {
      // Make the options appear and listen to the event, to react if the user selects
      // any of the options.
      this.menuSubscription = this.sidenavService.setContents([
        {
          name: 'actions.menu.terminal',
          actionName: 'terminal',
          icon: 'laptop'
        },
        {
          name: 'actions.menu.reboot',
          actionName: 'reboot',
          icon: 'rotate_right'
        },
        {
          name: 'actions.menu.update',
          actionName: 'update',
          icon: 'get_app',
        }
        // Options not implemented yet.
        /*
        {
          name: 'actions.menu.config',
          actionName: 'config',
          icon: 'settings',
          disabled: true
        }*/], [
        {
          name: !this.showingFullListInternal ? 'nodes.title' : 'node.title',
          actionName: 'back',
          icon: 'chevron_left'
        }]).subscribe(actionName => {
          // Call the adequate function if the user clicks any of the options.
          if (actionName === 'terminal') {
            this.terminal();
          } else if (actionName === 'config') {
            this.configuration();
          } else if (actionName === 'update') {
            this.update();
          } else if (actionName === 'reboot') {
            this.reboot();
          } else if (actionName === 'back') {
            this.back();
          }
        }
      );
    });
  }

  ngOnDestroy() {
    if (this.nodeSubscription) {
      this.nodeSubscription.unsubscribe();
    }
    if (this.menuSubscription) {
      this.menuSubscription.unsubscribe();
    }
    if (this.rebootSubscription) {
      this.rebootSubscription.unsubscribe();
    }
    if (this.updateSubscription) {
      this.updateSubscription.unsubscribe();
    }
  }

  reboot() {
    const confirmationDialog = GeneralUtils.createConfirmationDialog(this.dialog, 'actions.reboot.confirmation');

    confirmationDialog.componentInstance.operationAccepted.subscribe(() => {
      confirmationDialog.componentInstance.showProcessing();

      this.rebootSubscription = this.nodeService.reboot(NodeComponent.getCurrentNodeKey()).subscribe(() => {
        this.snackbarService.showDone('actions.reboot.done');
        confirmationDialog.close();
      }, (err: OperationError) => {
        err = processServiceError(err);

        confirmationDialog.componentInstance.showDone('confirmation.error-header-text', err.translatableErrorMsg);
      });
    });
  }

  update() {
    // Configuration for the confirmation modal window used as the main UI element for the
    // updating process.
    const confirmationData: ConfirmationData = {
      text: 'actions.update.processing',
      headerText: 'actions.update.title',
      confirmButtonText: 'actions.update.processing-button',
      disableDismiss: true,
    };

    // Show the confirmation window in a "loading" state while checking if there are updates.
    const config = new MatDialogConfig();
    config.data = confirmationData;
    config.autoFocus = false;
    config.width = AppConfig.smallModalWidth;
    const confirmationDialog = this.dialog.open(ConfirmationComponent, config);
    setTimeout(() => confirmationDialog.componentInstance.showProcessing());

    // Check if there is an update available.
    this.updateSubscription = this.nodeService.checkUpdate(NodeComponent.getCurrentNodeKey()).subscribe(response => {
      if (response && response.available) {
        // New configuration for asking for confirmation.
        const newVersion = this.translateService.instant('actions.update.version-change',
          { currentVersion: response.current_version, newVersion: response.available_version }
        );
        const newConfirmationData: ConfirmationData = {
          text: 'actions.update.update-available1',
          list: [newVersion],
          lowerText: 'actions.update.update-available2',
          headerText: 'actions.update.title',
          confirmButtonText: 'actions.update.install',
          cancelButtonText: 'common.cancel',
        };

        // Ask for confirmation.
        setTimeout(() => {
          confirmationDialog.componentInstance.showAsking(newConfirmationData);
        });
      } else if (response) {
        // Inform that there are no updates available.
        setTimeout(() => {
          confirmationDialog.componentInstance.showDone(null, 'actions.update.no-update', [response.current_version]);
        });
      } else {
        // Inform that there was an error.
        setTimeout(() => {
          confirmationDialog.componentInstance.showDone('confirmation.error-header-text', 'common.operation-error');
        });
      }
    }, (err: OperationError) => {
      err = processServiceError(err);

      // Must wait because the loading state is activated after a frame.
      setTimeout(() => {
        confirmationDialog.componentInstance.showDone('confirmation.error-header-text', err.translatableErrorMsg);
      });
    });

    // React if the user confirm the update.
    confirmationDialog.componentInstance.operationAccepted.subscribe(() => {
      confirmationDialog.componentInstance.showProcessing();

      // Update the visor.
      this.updateSubscription = this.nodeService.update(NodeComponent.getCurrentNodeKey()).subscribe(response => {
          confirmationDialog.componentInstance.data.lowerText = response.status;
      }, (err: OperationError) => {
        err = processServiceError(err);

        confirmationDialog.componentInstance.showDone('confirmation.error-header-text', err.translatableErrorMsg);
      },
        () => {
          this.snackbarService.showDone('actions.update.done');
          confirmationDialog.close();
        });
    });
  }

  configuration() {
    ConfigurationComponent.openDialog(this.dialog, {});
  }

  terminal() {
    const options: SelectableOption[] = [
      {
        icon: 'launch',
        label: 'actions.terminal-options.full',
      },
      {
        icon: 'open_in_browser',
        label: 'actions.terminal-options.simple',
      },
    ];

    SelectOptionComponent.openDialog(this.dialog, options, 'common.options').afterClosed().subscribe((selectedOption: number) => {
      if (selectedOption === 1) {
        // Open the complete terminal in a new tab.
        const protocol = window.location.protocol;
        const hostname = window.location.host.replace('localhost:4200', '127.0.0.1:8000');
        window.open(protocol + '//' + hostname + '/pty/' + NodeComponent.getCurrentNodeKey(), '_blank', 'noopener noreferrer');
      } else if (selectedOption === 2) {
        // Open the simple terminal in a modal window.
        BasicTerminalComponent.openDialog(this.dialog, {
          pk: NodeComponent.getCurrentNodeKey(),
          label: this.currentNode ? this.currentNode.label : '',
        });
      }
    });
  }

  back() {
    if (!this.showingFullListInternal) {
      this.router.navigate(['nodes']);
    } else {
      this.router.navigate(['nodes', NodeComponent.getCurrentNodeKey()]);
    }
  }
}
