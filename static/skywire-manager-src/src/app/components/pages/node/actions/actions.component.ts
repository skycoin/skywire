import { Component, AfterViewInit, OnDestroy, Input } from '@angular/core';
import { MatDialog } from '@angular/material/dialog';
import { Router } from '@angular/router';
import { Subscription } from 'rxjs';

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
import { TranslateService } from '@ngx-translate/core';
import { UpdateComponent } from 'src/app/components/layout/update/update.component';
import { StorageService } from 'src/app/services/storage.service';

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
    private storageService: StorageService,
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
        }], [
        {
          name: !this.showingFullListInternal ? 'nodes.title' : 'node.title',
          actionName: 'back',
          icon: 'chevron_left'
        }]).subscribe(actionName => {
          // Call the adequate function if the user clicks any of the options.
          if (actionName === 'terminal') {
            this.terminal();
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
    const labelInfo = this.storageService.getLabelInfo(NodeComponent.getCurrentNodeKey());
    const label = labelInfo ? labelInfo.label : '';
    UpdateComponent.openDialog(this.dialog, [{key: NodeComponent.getCurrentNodeKey(), label: label}]);
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
