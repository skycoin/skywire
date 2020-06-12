import { Component, OnDestroy, OnInit, NgZone } from '@angular/core';
import { Subscription, of, timer } from 'rxjs';
import { MatDialog } from '@angular/material/dialog';
import { Router } from '@angular/router';
import { delay, flatMap, tap } from 'rxjs/operators';

import { NodeService } from '../../../services/node.service';
import { Node } from '../../../app.datatypes';
import { AuthService } from '../../../services/auth.service';
import { EditLabelComponent } from '../../layout/edit-label/edit-label.component';
import { StorageService } from '../../../services/storage.service';
import { TabButtonData } from '../../layout/tab-bar/tab-bar.component';
import { SnackbarService } from '../../../services/snackbar.service';
import { SidenavService } from 'src/app/services/sidenav.service';
import { SelectColumnComponent, SelectedColumn } from '../../layout/select-column/select-column.component';
import GeneralUtils from 'src/app/utils/generalUtils';
import { SelectOptionComponent, SelectableOption } from '../../layout/select-option/select-option.component';
import { processServiceError } from 'src/app/utils/errors';

/**
 * List of the columns that can be used to sort the data.
 */
enum SortableColumns {
  State = 'transports.state',
  Label = 'nodes.label',
  Key = 'nodes.key',
}

/**
 * Page for showing the node list.
 */
@Component({
  selector: 'app-node-list',
  templateUrl: './node-list.component.html',
  styleUrls: ['./node-list.component.scss'],
})
export class NodeListComponent implements OnInit, OnDestroy {
  private static sortByInternal = SortableColumns.Key;
  private static sortReverseInternal = false;

  // Vars for keeping track of the column used for sorting the data.
  sortableColumns = SortableColumns;
  get sortBy(): SortableColumns { return NodeListComponent.sortByInternal; }
  set sortBy(val: SortableColumns) { NodeListComponent.sortByInternal = val; }
  get sortReverse(): boolean { return NodeListComponent.sortReverseInternal; }
  set sortReverse(val: boolean) { NodeListComponent.sortReverseInternal = val; }
  get sortingArrow(): string {
    return this.sortReverse ? 'keyboard_arrow_up' : 'keyboard_arrow_down';
  }

  loading = true;
  dataSource: Node[];
  tabsData: TabButtonData[] = [];

  private dataSubscription: Subscription;
  private updateTimeSubscription: Subscription;
  private menuSubscription: Subscription;

  // Vars for keeping track of the data updating.
  secondsSinceLastUpdate = 0;
  private lastUpdate = Date.now();
  updating = false;
  errorsUpdating = false;

  constructor(
    private nodeService: NodeService,
    private router: Router,
    private dialog: MatDialog,
    private authService: AuthService,
    public storageService: StorageService,
    private ngZone: NgZone,
    private snackbarService: SnackbarService,
    private sidenavService: SidenavService,
  ) {
    // Data for populating the tab bar.
    this.tabsData = [
      {
        icon: 'view_headline',
        label: 'nodes.title',
        linkParts: ['/nodes'],
      },
      {
        icon: 'settings',
        label: 'settings.title',
        linkParts: ['/settings'],
      }
    ];
  }

  ngOnInit() {
    // Load the data.
    this.refresh(0);

    // Procedure to keep updated the variable that indicates how long ago the data was updated.
    this.ngZone.runOutsideAngular(() => {
      this.updateTimeSubscription =
        timer(5000, 5000).subscribe(() => this.ngZone.run(() => {
          this.secondsSinceLastUpdate = Math.floor((Date.now() - this.lastUpdate) / 1000);
        }));
    });

    // Populate the left options bar.
    setTimeout(() => {
      this.menuSubscription = this.sidenavService.setContents([
        {
          name: 'common.logout',
          actionName: 'logout',
          icon: 'power_settings_new'
        }], null).subscribe(actionName => {
          // React to the events of the left options bar.
          if (actionName === 'logout') {
            this.logout();
          }
        }
      );
    });
  }

  ngOnDestroy() {
    this.dataSubscription.unsubscribe();
    this.updateTimeSubscription.unsubscribe();

    if (this.menuSubscription) {
      this.menuSubscription.unsubscribe();
    }
  }

  /**
   * Returns the scss class to be used to show the current status of the node.
   * @param forDot If true, returns a class for creating a colored dot. If false,
   * returns a class for a colored text.
   */
  nodeStatusClass(node: Node, forDot: boolean): string {
    switch (node.online) {
      case true:
        return forDot ? 'dot-green' : 'green-text';
      default:
        return forDot ? 'dot-red' : 'red-text';
    }
  }

  /**
   * Returns the text to be used to indicate the current status of the node.
   * @param forTooltip If true, returns a text for a tooltip. If false, returns a
   * text for the node list shown on small screens.
   */
  nodeStatusText(node: Node, forTooltip: boolean): string {
    switch (node.online) {
      case true:
        return 'node.statuses.online' + (forTooltip ? '-tooltip' : '');
      default:
        return 'node.statuses.offline' + (forTooltip ? '-tooltip' : '');
    }
  }

  /**
   * Changes the column and/or order used for sorting the data.
   */
  changeSortingOrder(column: SortableColumns) {
    if (this.sortBy !== column) {
      this.sortBy = column;
      this.sortReverse = false;
    } else {
      this.sortReverse = !this.sortReverse;
    }

    this.sortList();
  }

  /**
   * Opens the modal window used on small screens for selecting how to sort the data.
   */
  openSortingOrderModal() {
    // Get the list of sortable columns.
    const enumKeys = Object.keys(SortableColumns);
    const columnsMap = new Map<string, SortableColumns>();
    const columns = enumKeys.map(key => {
      const val = SortableColumns[key as any];
      columnsMap.set(val, SortableColumns[key]);

      return val;
    });

    SelectColumnComponent.openDialog(this.dialog, columns).afterClosed().subscribe((result: SelectedColumn) => {
      if (result) {
        if (columnsMap.has(result.label) && (result.sortReverse !== this.sortReverse || columnsMap.get(result.label) !== this.sortBy)) {
          this.sortBy = columnsMap.get(result.label);
          this.sortReverse = result.sortReverse;

          this.sortList();
        }
      }
    });
  }

  /**
   * Loads the data from the backend.
   * @param delayMilliseconds Delay before loading the data.
   * @param requestedManually True if the data is being loaded because of a direct request from the user.
   */
  private refresh(delayMilliseconds: number, requestedManually = false) {
    // Cancel any pending operation. Important because a previous operation could be waiting for
    // the delay to finish.
    if (this.dataSubscription) {
      this.dataSubscription.unsubscribe();
    }

    this.ngZone.runOutsideAngular(() => {
      this.dataSubscription = of(1).pipe(
        // Wait the requested delay.
        delay(delayMilliseconds),
        // Additional steps for making sure the UI shows the animation (important in case of quick errors).
        tap(() => this.ngZone.run(() => this.updating = true)),
        delay(120),
        // Load the data. The node pk is obtained from the currently openned node page.
        flatMap(() => this.nodeService.getNodes())
      ).subscribe(
        (nodes: Node[]) => {
          this.ngZone.run(() => {
            this.dataSource = nodes;
            this.sortList();
            this.loading = false;
            // Close any previous temporary loading error msg.
            this.snackbarService.closeCurrentIfTemporaryError();

            this.lastUpdate = Date.now();
            this.secondsSinceLastUpdate = 0;
            this.updating = false;
            this.errorsUpdating = false;

            if (requestedManually) {
              // Show a confirmation msg.
              this.snackbarService.showDone('common.refreshed', null);
            }

            // Automatically refresh the data after some time.
            this.refresh(this.storageService.getRefreshTime() * 1000);
          });
        }, err => {
          this.ngZone.run(() => {
            err = processServiceError(err);

            // Show an error msg if it has not be done before during the current attempt to obtain the data.
            if (!this.errorsUpdating) {
              if (this.loading) {
                this.snackbarService.showError('common.loading-error', null, true, err);
              } else {
                this.snackbarService.showError('nodes.error-load', null, true, err);
              }
            }

            // Stop the loading indicator and show a warning icon.
            this.updating = false;
            this.errorsUpdating = true;

            // Retry after some time. Do it faster if the component is still showing the
            // initial loading indicator (no data has been obtained since the component was created).
            if (this.loading) {
              this.refresh(3000, requestedManually);
            } else {
              this.refresh(this.storageService.getRefreshTime() * 1000, requestedManually);
            }
          });
        }
      );
    });
  }

  /**
   * Sorts the data.
   */
  private sortList() {
    this.dataSource = this.dataSource.sort((a, b) => {
      const defaultOrder = a.local_pk.localeCompare(b.local_pk);

      let response: number;
      if (this.sortBy === SortableColumns.Key) {
        response = !this.sortReverse ? a.local_pk.localeCompare(b.local_pk) : b.local_pk.localeCompare(a.local_pk);
      } else if (this.sortBy === SortableColumns.State) {
        if (a.online && !b.online) {
          response = -1;
        } else if (!a.online && b.online) {
          response = 1;
        }
        response = response * (this.sortReverse ? -1 : 1);
      } else if (this.sortBy === SortableColumns.Label) {
        response = !this.sortReverse ? a.label.localeCompare(b.label) : b.label.localeCompare(a.label);
      } else {
        response = defaultOrder;
      }

      return response !== 0 ? response : defaultOrder;
    });
  }

  logout() {
    this.authService.logout().subscribe(
      () => this.router.navigate(['login']),
      () => this.snackbarService.showError('common.logout-error')
    );
  }

  /**
   * Opens the modal window used on small screens with the options of an element.
   */
  showOptionsDialog(node: Node) {
    const options: SelectableOption[] = [
      {
        icon: 'short_text',
        label: 'edit-label.title',
      },
      {
        icon: 'close',
        label: 'nodes.delete-node',
      }
    ];

    SelectOptionComponent.openDialog(this.dialog, options).afterClosed().subscribe((selectedOption: number) => {
      if (selectedOption === 1) {
        this.showEditLabelDialog(node);
      } else if (selectedOption === 2) {
        this.deleteNode(node);
      }
    });
  }

  /**
   * Opens the modal window for changing the label of a node.
   */
  showEditLabelDialog(node: Node) {
    EditLabelComponent.openDialog(this.dialog, node).afterClosed().subscribe((changed: boolean) => {
      if (changed) {
        this.refresh(0);
      }
    });
  }

  /**
   * Removes an offline node from the list, until seeing it online again.
   */
  deleteNode(node: Node) {
    const confirmationDialog = GeneralUtils.createConfirmationDialog(this.dialog, 'nodes.delete-node-confirmation');

    confirmationDialog.componentInstance.operationAccepted.subscribe(() => {
      confirmationDialog.close();
      this.storageService.changeNodeState(node.local_pk, true);
      this.refresh(0);
      this.snackbarService.showDone('nodes.deleted');
    });
  }

  /**
   * Opens the page with the details of the node.
   */
  open(node: Node) {
    if (node.online) {
      this.router.navigate(['nodes', node.local_pk]);
    }
  }
}
