import { Component, OnDestroy, OnInit, NgZone } from '@angular/core';
import { Subscription, of, timer, forkJoin, Observable } from 'rxjs';
import { MatDialog, MatDialogConfig } from '@angular/material/dialog';
import { Router } from '@angular/router';
import { delay, flatMap, tap, catchError, mergeMap } from 'rxjs/operators';
import { TranslateService } from '@ngx-translate/core';

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
import { ClipboardService } from 'src/app/services/clipboard.service';
import { ConfirmationData, ConfirmationComponent } from '../../layout/confirmation/confirmation.component';
import { AppConfig } from 'src/app/app.config';
import { OperationError } from 'src/app/utils/operation-error';

/**
 * List of the columns that can be used to sort the data.
 */
enum SortableColumns {
  State = 'transports.state',
  Label = 'nodes.label',
  Key = 'nodes.key',
  DmsgServer = 'nodes.dmsg-server',
  Ping = 'nodes.ping',
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
  private static defaultSortableColumn = SortableColumns.Key;
  private static sortByInternal = NodeListComponent.defaultSortableColumn;
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
  showDmsgInfo = false;

  private readonly dmsgInfoStorageKey = 'show-dmsg-info';
  private dataSubscription: Subscription;
  private updateTimeSubscription: Subscription;
  private menuSubscription: Subscription;
  private updateSubscription: Subscription;

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
    private clipboardService: ClipboardService,
    private translateService: TranslateService,
  ) {
    // We only need to check if the key exists.
    this.showDmsgInfo = !!localStorage.getItem(this.dmsgInfoStorageKey);

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
          name: 'nodes.update-all',
          actionName: 'update',
          icon: 'get_app'
        },
        {
          name: 'common.logout',
          actionName: 'logout',
          icon: 'power_settings_new'
        }], null).subscribe(actionName => {
          // React to the events of the left options bar.
          if (actionName === 'logout') {
            this.logout();
          } else if (actionName === 'update') {
            this.updateAll();
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
    if (this.updateSubscription) {
      this.updateSubscription.unsubscribe();
    }
  }

  /**
   * Makes the dmsg info to be shown or hidden.
   */
  toggleDmsgInfo() {
    this.showDmsgInfo = !this.showDmsgInfo;
    // Create or remove an entry in the local storage, to be able to recover the setting later.
    if (this.showDmsgInfo) {
      localStorage.setItem(this.dmsgInfoStorageKey, '-');
    } else {
      localStorage.removeItem(this.dmsgInfoStorageKey);
    }

    // Do not allow to sort the list using a dmsg column while no dmsg data is being shown.
    if (this.sortBy === SortableColumns.DmsgServer || this.sortBy === SortableColumns.Ping) {
      this.changeSortingOrder(NodeListComponent.defaultSortableColumn);
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
    let columns = enumKeys.map(key => {
      // Ignore all dmsg columns if the dmsg data is not being shown.
      if (this.showDmsgInfo || (SortableColumns[key] !== SortableColumns.DmsgServer && SortableColumns[key] !== SortableColumns.Ping)) {
        const val = SortableColumns[key as any];
        columnsMap.set(val, SortableColumns[key]);

        return val;
      }
    });

    // Remove all empty entries.
    columns = columns.filter(val => val);

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
      } else if (this.sortBy === SortableColumns.DmsgServer) {
        response = !this.sortReverse ? a.dmsgServerPk.localeCompare(b.dmsgServerPk) : b.dmsgServerPk.localeCompare(a.dmsgServerPk);
      } else if (this.sortBy === SortableColumns.Ping) {
        response = !this.sortReverse ? a.roundTripPing - b.roundTripPing : b.roundTripPing - a.roundTripPing;
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

  // Updates all visors.
  updateAll() {
    if (!this.dataSource || this.dataSource.length === 0) {
      this.snackbarService.showError('nodes.update.no-visors');

      return;
    }

    // Configuration for the confirmation modal window used as the main UI element for the
    // updating process.
    const confirmationData: ConfirmationData = {
      text: 'nodes.update.processing',
      headerText: 'nodes.update.title',
      confirmButtonText: 'nodes.update.processing-button',
      disableDismiss: true,
    };

    // Show the confirmation window in a "loading" state while checking if there are updates.
    const config = new MatDialogConfig();
    config.data = confirmationData;
    config.autoFocus = false;
    config.width = AppConfig.smallModalWidth;
    const confirmationDialog = this.dialog.open(ConfirmationComponent, config);
    setTimeout(() => confirmationDialog.componentInstance.showProcessing());

    if (this.updateSubscription) {
      this.updateSubscription.unsubscribe();
    }

    const nodesToCheck = this.dataSource.filter(node => node.online).map(node => node.local_pk);

    // Check if there are updates available.
    this.updateSubscription = forkJoin(nodesToCheck.map(pk => this.nodeService.checkUpdate(pk))).subscribe(response => {
      // Check how many visors have to be updated.
      let visorsWithUpdate = 0;
      let currentVersion = '';
      let differentCurrentVersions = false;
      let newVersion = '';
      response.forEach(updateInfo => {
        if (updateInfo && updateInfo.available) {
          visorsWithUpdate += 1;

          if (currentVersion && currentVersion !== updateInfo.current_version) {
            differentCurrentVersions = true;
          }

          currentVersion = updateInfo.current_version;
          newVersion = updateInfo.available_version;
        }
      });

      if (visorsWithUpdate > 0) {
        // Text for asking for confirmation before updating.
        let newText: string;
        if (!differentCurrentVersions) {
          newText = this.translateService.instant('nodes.update.update-available',
            { number: visorsWithUpdate, currentVersion: currentVersion, newVersion: newVersion }
          );
        } else {
          newText = this.translateService.instant('nodes.update.update-available-different',
            { number: visorsWithUpdate, newVersion: newVersion }
          );
        }

        // New configuration for asking for confirmation.
        const newConfirmationData: ConfirmationData = {
          text: newText,
          headerText: 'nodes.update.title',
          confirmButtonText: 'nodes.update.install',
          cancelButtonText: 'common.cancel',
        };

        // Ask for confirmation.
        setTimeout(() => {
          confirmationDialog.componentInstance.showAsking(newConfirmationData);
        });
      } else {
        // Inform that there are no updates available.
        const newText = this.translateService.instant('nodes.update.no-update');
        setTimeout(() => {
          confirmationDialog.componentInstance.showDone(null, newText);
        });
      }
    }, (err: OperationError) => {
      err = processServiceError(err);

      // Must wait because the loading state is activated after a frame.
      setTimeout(() => {
        confirmationDialog.componentInstance.showDone('confirmation.error-header-text', err.translatableErrorMsg);
      });
    });

    // React if the user confirms the update.
    confirmationDialog.componentInstance.operationAccepted.subscribe(() => {
      confirmationDialog.componentInstance.showProcessing();

      // Keys and labels of all visors.
      const keys = this.dataSource.map(node => node.local_pk);
      const labels = this.dataSource.map(node => node.label);
      // Update all visors.
      this.updateSubscription = this.recursivelyUpdateWallets(keys, labels).subscribe(response => {
        if (response === 0) {
          // If everything was ok, show a confirmation.
          confirmationDialog.componentInstance.showDone('confirmation.done-header-text', 'nodes.update.done-all');
        } else if (response === this.dataSource.length) {
          // Error if no visor was updated.
          confirmationDialog.componentInstance.showDone('confirmation.error-header-text', 'nodes.update.all-failed-error');
        } else {
          // Error if only some visors were updated.
          confirmationDialog.componentInstance.showDone(
            'confirmation.error-header-text',
            this.translateService.instant('nodes.update.some-updated-error',
              {failedNumber: response, updatedNumber: this.dataSource.length - response}
            )
          );
        }
      });
    });
  }

  /**
   * Recursively updates the visors in the list. It returns how many visors the function was not
   * able to update.
   * @param keys Keys of the visors to update. The list will be altered by the function.
   * @param labels Labels of the visors to update. The list will be altered by the function.
   * @param errors Errors found during the process. For internal use.
   */
  private recursivelyUpdateWallets(keys: string[], labels: string[], errors = 0): Observable<number> {
    return this.nodeService.update(keys[keys.length - 1]).pipe(catchError(() => {
      // If there is a problem updating a visor, return null to be able to continue with
      // the process.
      return of(null);
    }), mergeMap(response => {
      // Show the result of the current step.
      if (response && response.updated) {
        this.snackbarService.showDone(this.translateService.instant('nodes.update.done', { name: labels[labels.length - 1] }));
      } else {
        this.snackbarService.showError(this.translateService.instant('nodes.update.update-error', { name: labels[labels.length - 1] }));
        errors += 1;
      }

      keys.pop();
      labels.pop();

      // Go to the next step.
      if (keys.length >= 1) {
        return this.recursivelyUpdateWallets(keys, labels, errors);
      }

      return of(errors);
    }));
  }

  /**
   * Opens the modal window used on small screens with the options of an element.
   */
  showOptionsDialog(node: Node) {
    const options: SelectableOption[] = [
      {
        icon: 'filter_none',
        label: 'nodes.copy-key',
      }
    ];

    if (this.showDmsgInfo) {
      options.push({
        icon: 'filter_none',
        label: 'nodes.copy-dmsg',
      });
    }

    options.push({
      icon: 'short_text',
      label: 'edit-label.title',
    });

    if (!node.online) {
      options.push({
        icon: 'close',
        label: 'nodes.delete-node',
      });
    }

    SelectOptionComponent.openDialog(this.dialog, options).afterClosed().subscribe((selectedOption: number) => {
      if (selectedOption === 1) {
        this.copySpecificTextToClipboard(node.local_pk);
      } else if (this.showDmsgInfo) {
        if (selectedOption === 2) {
          this.copySpecificTextToClipboard(node.dmsgServerPk);
        } else if (selectedOption === 3) {
          this.showEditLabelDialog(node);
        } else if (selectedOption === 4) {
          this.deleteNode(node);
        }
      } else {
        if (selectedOption === 2) {
          this.showEditLabelDialog(node);
        } else if (selectedOption === 3) {
          this.deleteNode(node);
        }
      }
    });
  }

  /**
   * Copies the public key of a visor. If the dmsg data is being shown, it allows the user to
   * select between copying the public key of the node or the dmsg server.
   */
  copyToClipboard(node: Node) {
    if (!this.showDmsgInfo) {
      this.copySpecificTextToClipboard(node.local_pk);
    } else {
      const options: SelectableOption[] = [
        {
          icon: 'filter_none',
          label: 'nodes.key',
        },
        {
          icon: 'filter_none',
          label: 'nodes.dmsg-server',
        }
      ];

      SelectOptionComponent.openDialog(this.dialog, options).afterClosed().subscribe((selectedOption: number) => {
        if (selectedOption === 1) {
          this.copySpecificTextToClipboard(node.local_pk);
        } else if (selectedOption === 2) {
          this.copySpecificTextToClipboard(node.dmsgServerPk);
        }
      });
    }
  }

  /**
   * Copies a text to the clipboard.
   * @param text Text to copy.
   */
  private copySpecificTextToClipboard(text: string) {
    if (this.clipboardService.copy(text)) {
      this.snackbarService.showDone('copy.copied');
    }
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
