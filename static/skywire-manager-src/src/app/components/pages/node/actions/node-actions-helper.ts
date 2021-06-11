import { MatDialog } from '@angular/material/dialog';
import { Router } from '@angular/router';
import { Subscription } from 'rxjs';
import { TranslateService } from '@ngx-translate/core';
import { Injector } from '@angular/core';

import { BasicTerminalComponent } from './basic-terminal/basic-terminal.component';
import { SnackbarService } from '../../../../services/snackbar.service';
import { Node } from '../../../../app.datatypes';
import GeneralUtils from 'src/app/utils/generalUtils';
import { NodeService } from 'src/app/services/node.service';
import { OperationError } from 'src/app/utils/operation-error';
import { processServiceError } from 'src/app/utils/errors';
import { SelectableOption, SelectOptionComponent } from 'src/app/components/layout/select-option/select-option.component';
import { MenuOptionData } from 'src/app/components/layout/top-bar/top-bar.component';
import { UpdateComponent } from 'src/app/components/layout/update/update.component';
import { StorageService } from 'src/app/services/storage.service';

/**
 * Helper object for managing the options shown in the menu while in the node details page.
 */
export class NodeActionsHelper {
  /**
   * Allows to know if the currently displayed subpage is one dedicated to show a full list
   * of elements (true) or if it is one dedicated only to show a summary (false).
   */
  private showingFullList: boolean;
  private currentNode: Node;
  private currentNodeKey: string;

  options: MenuOptionData[] = [];
  returnButtonText: string;

  private rebootSubscription: Subscription;
  private updateSubscription: Subscription;

  // Services this class need.
  private dialog: MatDialog;
  private router: Router;
  private snackbarService: SnackbarService;
  private nodeService: NodeService;
  private translateService: TranslateService;
  private storageService: StorageService;

  constructor(injector: Injector, showingFullList: boolean) {
    // Get the services.
    this.dialog = injector.get(MatDialog);
    this.router = injector.get(Router);
    this.snackbarService = injector.get(SnackbarService);
    this.nodeService = injector.get(NodeService);
    this.translateService = injector.get(TranslateService);
    this.storageService = injector.get(StorageService);

    // Options for the menu shown in the top bar.
    this.options = [
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
      },
      {
        name: 'actions.menu.logs',
        actionName: 'logs',
        icon: 'subject',
      }
    ];

    this.showingFullList = showingFullList;
    this.returnButtonText = !showingFullList ? 'nodes.title' : 'node.title';
  }

  /**
   * Allows to set the data of the current node.
   */
  setCurrentNode(currentNode: Node) {
    this.currentNode = currentNode;
  }

  /**
   * Allows to set the key of the current node.
   */
  setCurrentNodeKey(nodeKey: string) {
    this.currentNodeKey = nodeKey;
  }

  /**
   * Must be called when an option form the top bar is selected.
   * @param actionName Name of the selected option, as defined in the options array.
   */
  performAction(actionName: string, nodePk: string) {
    // Call the adequate function if the user clicks any of the options.
    if (actionName === 'terminal') {
      this.terminal();
    } else if (actionName === 'update') {
      this.update();
    } else if (actionName === 'logs') {
      window.open(window.location.origin + '/api/visors/' + nodePk + '/runtime-logs', '_blank');
    } else if (actionName === 'reboot') {
      this.reboot();
    } else if (actionName === null) {
      // Null is returned if the back button was pressed.
      this.back();
    }
  }

  /**
   * Cleans the object. Must be called when the object is no longer needed.
   */
  dispose() {
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

      this.rebootSubscription = this.nodeService.reboot(this.currentNodeKey).subscribe(() => {
        this.snackbarService.showDone('actions.reboot.done');
        confirmationDialog.close();
      }, (err: OperationError) => {
        err = processServiceError(err);

        confirmationDialog.componentInstance.showDone('confirmation.error-header-text', err.translatableErrorMsg);
      });
    });
  }

  update() {
    const labelInfo = this.storageService.getLabelInfo(this.currentNodeKey);
    const label = labelInfo ? labelInfo.label : '';
    UpdateComponent.openDialog(this.dialog, [{key: this.currentNodeKey, label: label}]);
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
        window.open(protocol + '//' + hostname + '/pty/' + this.currentNodeKey, '_blank', 'noopener noreferrer');
      } else if (selectedOption === 2) {
        // Open the simple terminal in a modal window.
        BasicTerminalComponent.openDialog(this.dialog, {
          pk: this.currentNodeKey,
          label: this.currentNode ? this.currentNode.label : '',
        });
      }
    });
  }

  back() {
    if (!this.showingFullList) {
      this.router.navigate(['nodes']);
    } else {
      this.router.navigate(['nodes', this.currentNodeKey]);
    }
  }
}
