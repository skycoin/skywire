import { Component, OnDestroy, OnInit, NgZone } from '@angular/core';
import { Subscription, of, timer, forkJoin, Observable } from 'rxjs';
import { MatDialog, MatDialogConfig } from '@angular/material/dialog';
import { Router, ActivatedRoute } from '@angular/router';
import { catchError, mergeMap } from 'rxjs/operators';
import { TranslateService } from '@ngx-translate/core';

import { NodeService, BackendData } from '../../../services/node.service';
import { Node } from '../../../app.datatypes';
import { AuthService } from '../../../services/auth.service';
import { EditLabelComponent } from '../../layout/edit-label/edit-label.component';
import { StorageService, LabeledElementTypes } from '../../../services/storage.service';
import { TabButtonData } from '../../layout/tab-bar/tab-bar.component';
import { SnackbarService } from '../../../services/snackbar.service';
import { SidenavService } from 'src/app/services/sidenav.service';
import GeneralUtils from 'src/app/utils/generalUtils';
import { SelectOptionComponent, SelectableOption } from '../../layout/select-option/select-option.component';
import { processServiceError } from 'src/app/utils/errors';
import { ClipboardService } from 'src/app/services/clipboard.service';
import { ConfirmationData, ConfirmationComponent } from '../../layout/confirmation/confirmation.component';
import { AppConfig } from 'src/app/app.config';
import { OperationError } from 'src/app/utils/operation-error';
import { FilterProperties, FilterFieldTypes } from 'src/app/utils/filters';
import { LabeledElementTextComponent } from '../../layout/labeled-element-text/labeled-element-text.component';
import { SortingModes, SortingColumn, DataSorter } from 'src/app/utils/lists/data-sorter';
import { DataFilterer } from 'src/app/utils/lists/data-filterer';

/**
 * Page for showing the node list.
 */
@Component({
  selector: 'app-node-list',
  templateUrl: './node-list.component.html',
  styleUrls: ['./node-list.component.scss'],
})
export class NodeListComponent implements OnInit, OnDestroy {
  // Small texts for identifying the list, needed for the helper objects.
  private readonly nodesListId = 'nl';
  private readonly dmsgListId = 'dl';

  // Vars with the data of the columns used for sorting the data.
  stateSortData = new SortingColumn(['online'], 'transports.state', SortingModes.Boolean);
  labelSortData = new SortingColumn(['label'], 'nodes.label', SortingModes.Text);
  keySortData = new SortingColumn(['local_pk'], 'nodes.key', SortingModes.Text);
  dmsgServerSortData = new SortingColumn(['dmsgServerPk'], 'nodes.dmsg-server', SortingModes.Text);
  pingSortData = new SortingColumn(['roundTripPing'], 'nodes.ping', SortingModes.Number);

  private dataSortedSubscription: Subscription;
  private dataFiltererSubscription: Subscription;
  // Objects in charge of sorting and filtering the data.
  dataSorter: DataSorter;
  dataFilterer: DataFilterer;

  loading = true;
  dataSource: Node[];
  tabsData: TabButtonData[] = [];
  showDmsgInfo = false;

  // Vars for the pagination functionality.
  allNodes: Node[];
  filteredNodes: Node[];
  nodesToShow: Node[];
  numberOfPages = 1;
  currentPage = 1;
  // Used as a helper var, as the URL is read asynchronously.
  currentPageInUrl = 1;

  // Array with the properties of the columns that can be used for filtering the data.
  filterProperties: FilterProperties[] = [
    {
      filterName: 'nodes.filter-dialog.online',
      keyNameInElementsArray: 'online',
      type: FilterFieldTypes.Select,
      printableLabelsForValues: [
        {
          value: '',
          label: 'nodes.filter-dialog.online-options.any',
        },
        {
          value: 'true',
          label: 'nodes.filter-dialog.online-options.online',
        },
        {
          value: 'false',
          label: 'nodes.filter-dialog.online-options.offline',
        }
      ],
    },
    {
      filterName: 'nodes.filter-dialog.label',
      keyNameInElementsArray: 'label',
      type: FilterFieldTypes.TextInput,
      maxlength: 100,
    },
    {
      filterName: 'nodes.filter-dialog.key',
      keyNameInElementsArray: 'local_pk',
      type: FilterFieldTypes.TextInput,
      maxlength: 66,
    },
    {
      filterName: 'nodes.filter-dialog.dmsg',
      keyNameInElementsArray: 'dmsgServerPk',
      secondaryKeyNameInElementsArray: 'dmsgServerPk_label',
      type: FilterFieldTypes.TextInput,
      maxlength: 66,
    }
  ];

  private dataSubscription: Subscription;
  private updateTimeSubscription: Subscription;
  private menuSubscription: Subscription;
  private updateSubscription: Subscription;
  private navigationsSubscription: Subscription;
  private languageSubscription: Subscription;

  // Vars for keeping track of the data updating.
  secondsSinceLastUpdate = 0;
  private lastUpdate = Date.now();
  updating = false;
  errorsUpdating = false;
  // True if the user manually requested the data to be updated and the update has still
  // not been made.
  lastUpdateRequestedManually = false;

  labeledElementTypes = LabeledElementTypes;

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
    route: ActivatedRoute,
  ) {
    // Show the dmsg info if the dmsg url was used.
    this.showDmsgInfo = this.router.url.indexOf('dmsg') !== -1;

    // Remove the DMSG filtering options if no DMSG info is being shown.
    if (!this.showDmsgInfo) {
      this.filterProperties.splice(this.filterProperties.length - 1);
    }

    // Initialize the data sorter.
    const sortableColumns: SortingColumn[] = [
      this.stateSortData,
      this.labelSortData,
      this.keySortData,
    ];
    if (this.showDmsgInfo) {
      sortableColumns.push(this.dmsgServerSortData);
      sortableColumns.push(this.pingSortData);
    }
    this.dataSorter = new DataSorter(
      this.dialog, this.translateService, sortableColumns, 2, this.showDmsgInfo ? this.dmsgListId : this.nodesListId
    );
    this.dataSortedSubscription = this.dataSorter.dataSorted.subscribe(() => {
      // When this happens, the data in allNodes has already been sorted.
      this.recalculateElementsToShow();
    });

    this.dataFilterer = new DataFilterer(
      this.dialog, route, this.router, this.filterProperties, this.showDmsgInfo ? this.dmsgListId : this.nodesListId
    );
    this.dataFiltererSubscription = this.dataFilterer.dataFiltered.subscribe(data => {
      this.filteredNodes = data;
      this.dataSorter.setData(this.filteredNodes);
    });

    // Get the page requested in the URL.
    this.navigationsSubscription = route.paramMap.subscribe(params => {
      if (params.has('page')) {
        let selectedPage = Number.parseInt(params.get('page'), 10);
        if (isNaN(selectedPage) || selectedPage < 1) {
          selectedPage = 1;
        }

        this.currentPageInUrl = selectedPage;

        this.recalculateElementsToShow();
      }
    });

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

    // Refresh the data after languaje changes, to ensure the labels used for filtering
    // are updated.
    this.languageSubscription = this.translateService.onLangChange.subscribe(() => {
      this.nodeService.forceNodeListRefresh();
    });
  }

  ngOnInit() {
    // Load the data.
    this.nodeService.startRequestingNodeList();
    this.startGettingData();

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
    this.nodeService.stopRequestingNodeList();
    this.dataSubscription.unsubscribe();
    this.updateTimeSubscription.unsubscribe();
    this.navigationsSubscription.unsubscribe();
    this.languageSubscription.unsubscribe();

    if (this.menuSubscription) {
      this.menuSubscription.unsubscribe();
    }
    if (this.updateSubscription) {
      this.updateSubscription.unsubscribe();
    }

    this.dataSortedSubscription.unsubscribe();
    this.dataSorter.dispose();
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
   * Makes the node list to be immediately refreshed.
   * @param requestedManually True if the data is going to be loaded because of a direct request
   * from the user.
   */
  forceDataRefresh(requestedManually = false) {
    if (requestedManually) {
      this.lastUpdateRequestedManually = true;
    }

    this.nodeService.forceNodeListRefresh();
  }

  /**
   * Starts getting the data from the backend.
   */
  private startGettingData() {
    // Detect when the service is updating the data.
    this.dataSubscription = this.nodeService.updatingNodeList.subscribe(val => this.updating = val);

    this.ngZone.runOutsideAngular(() => {
      // Get the node list.
      this.dataSubscription.add(this.nodeService.nodeList.subscribe((result: BackendData) => {
        this.ngZone.run(() => {
          if (result) {
            // If the data was obtained.
            if (result.data) {
              this.allNodes = result.data as Node[];
              if (this.showDmsgInfo) {
                // Add the label data to the array, to be able to use it for filtering.
                this.allNodes.forEach(node => {
                  node['dmsgServerPk_label'] =
                    LabeledElementTextComponent.getCompleteLabel(this.storageService, this.translateService, node.dmsgServerPk);
                });
              }
              this.dataFilterer.setData(this.allNodes);

              this.loading = false;
              // Close any previous temporary loading error msg.
              this.snackbarService.closeCurrentIfTemporaryError();

              this.lastUpdate = result.momentOfLastCorrectUpdate;
              this.secondsSinceLastUpdate = Math.floor((Date.now() - result.momentOfLastCorrectUpdate) / 1000);
              this.errorsUpdating = false;

              if (this.lastUpdateRequestedManually) {
                // Show a confirmation msg.
                this.snackbarService.showDone('common.refreshed', null);
                this.lastUpdateRequestedManually = false;
              }

            // If there was an error while obtaining the data.
            } else if (result.error) {
              // Show an error msg if it has not be done before during the current attempt to obtain the data.
              if (!this.errorsUpdating) {
                if (this.loading) {
                  this.snackbarService.showError('common.loading-error', null, true, result.error);
                } else {
                  this.snackbarService.showError('nodes.error-load', null, true, result.error);
                }
              }

              // Stop the loading indicator and show a warning icon.
              this.errorsUpdating = true;
            }
          }
        });
      }));
    });
  }

  /**
   * Recalculates which elements should be shown on the UI.
   */
  private recalculateElementsToShow() {
    // Needed to prevent racing conditions.
    this.currentPage = this.currentPageInUrl;

    // Needed to prevent racing conditions.
    if (this.filteredNodes) {
      // Calculate the pagination values.
      const maxElements = AppConfig.maxFullListElements;
      this.numberOfPages = Math.ceil(this.filteredNodes.length / maxElements);
      if (this.currentPage > this.numberOfPages) {
        this.currentPage = this.numberOfPages;
      }

      // Limit the elements to show.
      const start = maxElements * (this.currentPage - 1);
      const end = start + maxElements;
      this.nodesToShow = this.filteredNodes.slice(start, end);
    } else {
      this.nodesToShow = null;
    }

    this.dataSource = this.nodesToShow;
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

    // Get the list of all online visors, to check if there are updates available.
    const nodesToCheck: string[] = [];
    const labelsToCheck: string[] = [];
    this.dataSource.forEach(node => {
      if (node.online) {
        nodesToCheck.push(node.local_pk);
        labelsToCheck.push(node.label);
      }
    });

    // Keys and labels of all visors with an update available.
    const keysWithUpdate: string[] = [];
    const labelsWithUpdate: string[] = [];
    // How many visors have an update available.
    let visorsWithUpdate = 0;

    // Check if there are updates available.
    this.updateSubscription = forkJoin(nodesToCheck.map(pk => this.nodeService.checkUpdate(pk))).subscribe(response => {
      // Contains the list of all updates found, without repetitions.
      const updates = new Map<string, string>();

      // Check the response for each visor.
      response.forEach((updateInfo, i) => {
        if (updateInfo && updateInfo.available) {
          visorsWithUpdate += 1;

          // Save the data for calling the update procedure later.
          keysWithUpdate.push(nodesToCheck[i]);
          labelsWithUpdate.push(labelsToCheck[i]);

          // Save the name of the update, if it was not found before.
          if (!updates.has(updateInfo.current_version + updateInfo.available_version)) {
            const newVersion = this.translateService.instant('nodes.update.version-change',
              { currentVersion: updateInfo.current_version, newVersion: updateInfo.available_version }
            );

            updates.set(updateInfo.current_version + updateInfo.available_version, newVersion);
          }
        }
      });

      if (visorsWithUpdate > 0) {
        // Text for asking for confirmation before updating.
        let newText: string;
        if (visorsWithUpdate === 1) {
          newText = 'nodes.update.update-available-single';
        } else {
          newText = this.translateService.instant('nodes.update.update-available-multiple', {number: visorsWithUpdate});
        }

        const updatesList: string[] = [];
        updates.forEach(u => updatesList.push(u));

        // New configuration for asking for confirmation.
        const newConfirmationData: ConfirmationData = {
          text: newText,
          list: updatesList,
          lowerText: 'nodes.update.update-available-confirmation',
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

      // Update all visors.
      this.updateSubscription = this.recursivelyUpdateWallets(keysWithUpdate, labelsWithUpdate).subscribe(response => {
        if (response === 0) {
          // If everything was ok, show a confirmation.
          confirmationDialog.componentInstance.showDone('confirmation.done-header-text', 'nodes.update.done-all');
        } else if (response === visorsWithUpdate) {
          // Error if no visor was updated.
          confirmationDialog.componentInstance.showDone('confirmation.error-header-text', 'nodes.update.all-failed-error');
        } else {
          // Error if only some visors were updated.
          confirmationDialog.componentInstance.showDone(
            'confirmation.error-header-text',
            this.translateService.instant('nodes.update.some-updated-error',
              {failedNumber: response, updatedNumber: visorsWithUpdate - response}
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
      label: 'labeled-element.edit-label',
    });

    if (!node.online) {
      options.push({
        icon: 'close',
        label: 'nodes.delete-node',
      });
    }

    SelectOptionComponent.openDialog(this.dialog, options, 'common.options').afterClosed().subscribe((selectedOption: number) => {
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

      SelectOptionComponent.openDialog(this.dialog, options, 'common.options').afterClosed().subscribe((selectedOption: number) => {
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
    let labelInfo =  this.storageService.getLabelInfo(node.local_pk);
    if (!labelInfo) {
      labelInfo = {
        id: node.local_pk,
        label: '',
        identifiedElementType: LabeledElementTypes.Node,
      };
    }

    EditLabelComponent.openDialog(this.dialog, labelInfo).afterClosed().subscribe((changed: boolean) => {
      if (changed) {
        this.forceDataRefresh();
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
      this.storageService.setLocalNodesAsHidden([node.local_pk]);
      this.forceDataRefresh();
      this.snackbarService.showDone('nodes.deleted');
    });
  }

  /**
   * Removes all offline nodes from the list, until seeing them online again.
   */
  removeOffline() {
    const confirmationDialog = GeneralUtils.createConfirmationDialog(this.dialog, 'nodes.delete-all-offline-confirmation');

    confirmationDialog.componentInstance.operationAccepted.subscribe(() => {
      confirmationDialog.close();

      // Prepare all offline nodes to be removed.
      const nodesToRemove: string[] = [];
      this.allNodes.forEach(node => {
        if (!node.online) {
          nodesToRemove.push(node.local_pk);
        }
      });

      // Remove the nodes and show the result.
      if (nodesToRemove.length > 0) {
        this.storageService.setLocalNodesAsHidden(nodesToRemove);

        this.forceDataRefresh();

        if (nodesToRemove.length === 1) {
          this.snackbarService.showDone('nodes.deleted-singular');
        } else {
          this.snackbarService.showDone('nodes.deleted-plural', { number: nodesToRemove.length });
        }
      } else {
        this.snackbarService.showWarning('nodes.no-offline-nodes');
      }
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
