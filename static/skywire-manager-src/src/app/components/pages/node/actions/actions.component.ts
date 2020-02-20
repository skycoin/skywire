import { Component, AfterViewInit, OnDestroy, Input } from '@angular/core';
import { MatDialog } from '@angular/material/dialog';
import { Router } from '@angular/router';
import { Subscription } from 'rxjs';

import { ConfigurationComponent } from './configuration/configuration.component';
import { UpdateNodeComponent } from './update-node/update-node.component';
import { BasicTerminalComponent } from './basic-terminal/basic-terminal.component';
import { SnackbarService } from '../../../../services/snackbar.service';
import { NodeComponent } from '../node.component';
import { SidenavService } from 'src/app/services/sidenav.service';
import { Node } from '../../../../app.datatypes';
import GeneralUtils from 'src/app/utils/generalUtils';
import { NodeService } from 'src/app/services/node.service';

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

  constructor(
    private dialog: MatDialog,
    private router: Router,
    private snackbarService: SnackbarService,
    private sidenavService: SidenavService,
    private nodeService: NodeService,
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
        }
        // Options not implemented yet.
        /*
        {
          name: 'actions.menu.config',
          actionName: 'config',
          icon: 'settings',
          disabled: true
        },
        {
          name: 'actions.menu.update',
          actionName: 'update',
          icon: 'get_app',
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
  }

  reboot() {
    const confirmationDialog = GeneralUtils.createConfirmationDialog(this.dialog, 'actions.reboot.confirmation');

    confirmationDialog.componentInstance.operationAccepted.subscribe(() => {
      confirmationDialog.componentInstance.showProcessing();

      this.rebootSubscription = this.nodeService.reboot(this.currentNode.local_pk).subscribe(() => {
        this.snackbarService.showDone('actions.reboot.done');
        confirmationDialog.close();
      }, () => {
        confirmationDialog.componentInstance.showDone('confirmation.error-header-text', 'common.operation-error');
      });
    });
  }

  update() {
    UpdateNodeComponent.openDialog(this.dialog).afterClosed().subscribe((updated) => {
      if (updated) {
        this.snackbarService.showDone('actions.update.update-success');
      }
    });
  }

  configuration() {
    ConfigurationComponent.openDialog(this.dialog, {});
  }

  terminal() {
    BasicTerminalComponent.openDialog(this.dialog, {
      pk: this.currentNode.local_pk,
      label: this.currentNode.label,
    });
  }

  back() {
    if (!this.showingFullListInternal) {
      this.router.navigate(['nodes']);
    } else {
      this.router.navigate(['nodes', this.currentNode.local_pk]);
    }
  }
}
