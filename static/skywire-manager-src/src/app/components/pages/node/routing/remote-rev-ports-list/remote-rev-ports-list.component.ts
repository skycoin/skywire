import { Component, Input, OnDestroy } from '@angular/core';
import { MatDialog, MatDialogRef } from '@angular/material/dialog';
import { Observable, Subscription } from 'rxjs';
import { ActivatedRoute, Router } from '@angular/router';
import { TranslateService } from '@ngx-translate/core';

import { Node, RemoteRevPort } from '../../../../../app.datatypes';
import { NodeComponent } from '../../node.component';
import { AppConfig } from '../../../../../app.config';
import { ConfirmationComponent } from '../../../../layout/confirmation/confirmation.component';
import GeneralUtils from '../../../../../utils/generalUtils';
import { SnackbarService } from '../../../../../services/snackbar.service';
import { SelectableOption, SelectOptionComponent } from 'src/app/components/layout/select-option/select-option.component';
import { processServiceError } from 'src/app/utils/errors';
import { OperationError } from 'src/app/utils/operation-error';
import { FilterProperties, FilterFieldTypes } from 'src/app/utils/filters';
import { StorageService } from 'src/app/services/storage.service';
import { LabeledElementTextComponent } from 'src/app/components/layout/labeled-element-text/labeled-element-text.component';
import { DataSorter, SortingColumn, SortingModes } from 'src/app/utils/lists/data-sorter';
import { DataFilterer } from 'src/app/utils/lists/data-filterer';
import { CreateRemoteRevPortComponent } from './create-remote-rev-port/create-remote-rev-port.component';
import { FwdService } from 'src/app/services/fwd.service';

/**
 * Shows the list of remote port connections of a node. I can be used to show a short preview, with just some
 * elements and a link for showing the rest: or the full list, with pagination controls.
 */
@Component({
  selector: 'app-remote-rev-ports-list',
  templateUrl: './remote-rev-ports-list.component.html',
  styleUrls: ['./remote-rev-ports-list.component.scss']
})
export class RemoteRevPortsListComponent implements OnDestroy {
  // Small text for identifying the list, needed for the helper objects.
  private readonly listId = 'lr';

  nodePK: string;

  // Vars with the data of the columns used for sorting the data.
  IDSortData = new SortingColumn(['connectionID'], 'remote-rev-ports.connection-id', SortingModes.Text, ['id_label']);
  remotePortSortData = new SortingColumn(['remotePortNumber'], 'remote-rev-ports.remote-port-number', SortingModes.Number);
  localPortSortData = new SortingColumn(['localPortNumber'], 'remote-rev-ports.local-port-number', SortingModes.Number);

  private dataSortedSubscription: Subscription;
  private dataFiltererSubscription: Subscription;
  // Objects in charge of sorting and filtering the data.
  dataSorter: DataSorter;
  dataFilterer: DataFilterer;

  dataSource: RemoteRevPort[];
  /**
   * Keeps track of the state of the check boxes of the elements.
   */
  selections = new Map<string, boolean>();

  /**
   * If true, the control can only show few elements and, if there are more elements, a link for
   * accessing the full list. If false, the full list is shown, with pagination
   * controls, if needed.
   */
  showShortList_: boolean;
  @Input() set showShortList(val: boolean) {
    this.showShortList_ = val;
    // Sort the data.
    this.dataSorter.setData(this.filteredPorts);
  }

  currentNode: Node;
  allPorts: RemoteRevPort[];
  filteredPorts: RemoteRevPort[];
  PortsToShow: RemoteRevPort[];
  numberOfPages = 1;
  currentPage = 1;
  // Used as a helper var, as the URL is readed asynchronously.
  currentPageInUrl = 1;
  @Input() set node(val: Node) {
    this.currentNode = val;
    this.allPorts = val.remoteConnectedPorts;
    this.nodePK = val.localPk;

    // Add the label data to the array, to be able to use it for filtering and sorting.
    this.allPorts.forEach(port => {
      port['id_label'] =
        LabeledElementTextComponent.getCompleteLabel(this.storageService, this.translateService, port.connectionID);
    });

    this.dataFilterer.setData(this.allPorts);
  }

  // Array with the properties of the columns that can be used for filtering the data.
  filterProperties: FilterProperties[] = [
    {
      filterName: 'remote-rev-ports.filter-dialog.id',
      keyNameInElementsArray: 'connectionID',
      secondaryKeyNameInElementsArray: 'id_label',
      type: FilterFieldTypes.TextInput,
      maxlength: 66,
    },
    {
      filterName: 'remote-rev-ports.filter-dialog.remote-port',
      keyNameInElementsArray: 'remotePortNumber',
      type: FilterFieldTypes.TextInput,
      maxlength: 7,
    },
    {
      filterName: 'remote-rev-ports.filter-dialog.local-port',
      keyNameInElementsArray: 'localPortNumber',
      type: FilterFieldTypes.TextInput,
      maxlength: 7,
    }
  ];

  //private persistentTransportSubscription: Subscription;
  private navigationsSubscription: Subscription;
  private operationSubscriptionsGroup: Subscription[] = [];
  private languageSubscription: Subscription;

  constructor(
    private dialog: MatDialog,
    private fwdService: FwdService,
    private route: ActivatedRoute,
    private router: Router,
    private snackbarService: SnackbarService,
    private translateService: TranslateService,
    private storageService: StorageService,
  ) {
    // Initialize the data sorter.
    const sortableColumns: SortingColumn[] = [
      this.IDSortData,
      this.remotePortSortData,
      this.localPortSortData,
    ];

    this.dataSorter = new DataSorter(this.dialog, this.translateService, this.storageService, sortableColumns, 2, this.listId);
    this.dataSortedSubscription = this.dataSorter.dataSorted.subscribe(() => {
      // When this happens, the data in allPorts has already been sorted.
      this.recalculateElementsToShow();
    });

    this.dataFilterer = new DataFilterer(this.dialog, this.route, this.router, this.filterProperties, this.listId);
    this.dataFiltererSubscription = this.dataFilterer.dataFiltered.subscribe(data => {
      this.filteredPorts = data;

      this.dataSorter.setData(this.filteredPorts);
    });

    // Get the page requested in the URL.
    this.navigationsSubscription = this.route.paramMap.subscribe(params => {
      if (params.has('page')) {
        let selectedPage = Number.parseInt(params.get('page'), 10);
        if (isNaN(selectedPage) || selectedPage < 1) {
          selectedPage = 1;
        }

        this.currentPageInUrl = selectedPage;

        this.recalculateElementsToShow();
      }
    });

    // Refresh the data after languaje changes, to ensure the labels used for filtering
    // are updated.
    this.languageSubscription = this.translateService.onLangChange.subscribe(() => {
      this.node = this.currentNode;
    });
  }

  ngOnDestroy() {
    this.navigationsSubscription.unsubscribe();
    this.operationSubscriptionsGroup.forEach(sub => sub.unsubscribe());
    this.languageSubscription.unsubscribe();

    this.dataSortedSubscription.unsubscribe();
    this.dataSorter.dispose();

    this.dataFiltererSubscription.unsubscribe();
    this.dataFilterer.dispose();
  }

  /**
   * Changes the selection state of an entry (modifies the state of its checkbox).
   */
  changeSelection(port: RemoteRevPort) {
    if (this.selections.get(port.connectionID)) {
      this.selections.set(port.connectionID, false);
    } else {
      this.selections.set(port.connectionID, true);
    }
  }

  /**
   * Checks if at lest one entry has been selected via its checkbox.
   */
  hasSelectedElements(): boolean {
    if (!this.selections) {
      return false;
    }

    let found = false;
    this.selections.forEach((val) => {
      if (val) {
        found = true;
      }
    });

    return found;
  }

  /**
   * Selects or deselects all items.
   */
  changeAllSelections(setSelected: boolean) {
    this.selections.forEach((val, key) => {
      this.selections.set(key, setSelected);
    });
  }

  /**
   * Deletes the selected elements.
   */
  deleteSelected() {
    // Ask for confirmation.
    const confirmationDialog = GeneralUtils.createConfirmationDialog(this.dialog, 'remote-rev-ports.delete-selected-confirmation');

    confirmationDialog.componentInstance.operationAccepted.subscribe(() => {
      confirmationDialog.componentInstance.showProcessing();

      const elementsToRemove: string[] = [];
      this.selections.forEach((val, key) => {
        if (val) {
          elementsToRemove.push(key);
        }
      });

      this.deleteRecursively(elementsToRemove, confirmationDialog);
    });
  }

  /**
   * Shows the remote port connection creation modal window.
   */
  create() {
    CreateRemoteRevPortComponent.openDialog(this.dialog);
  }

  /**
   * Opens the modal window used on small screens with the options of an element.
   */
  showOptionsDialog(port: RemoteRevPort) {
    const options: SelectableOption[] = [];
    options.push({
      icon: 'close',
      label: 'remote-rev-ports.delete',
    });

    SelectOptionComponent.openDialog(this.dialog, options, 'common.options').afterClosed().subscribe((selectedOption: number) => {
      if (selectedOption === 1) {
        this.delete(port);
      }
    });
  }

  /**
   * Deletes a specific element.
   */
  delete(port: RemoteRevPort) {
    const confirmationMsg = 'remote-rev-ports.delete-confirmation';
    const confirmationDialog = GeneralUtils.createConfirmationDialog(this.dialog, confirmationMsg);

    confirmationDialog.componentInstance.operationAccepted.subscribe(() => {
      confirmationDialog.componentInstance.showProcessing();

      // Start the operation and save it for posible cancellation.
      this.operationSubscriptionsGroup.push(this.startDeleting(port.connectionID).subscribe(() => {
        confirmationDialog.close();
        // Make the parent page reload the data.
        NodeComponent.refreshCurrentDisplayedData();
        this.snackbarService.showDone('remote-rev-ports.deleted');
      }, (err: OperationError) => {
        err = processServiceError(err);
        confirmationDialog.componentInstance.showDone('confirmation.error-header-text', err.translatableErrorMsg);
      }));
    });
  }

  /**
   * Asks the node data to be refreshed.
   */
  refreshData() {
    NodeComponent.refreshCurrentDisplayedData();
  }

  /**
   * Recalculates which elements should be shown on the UI.
   */
  private recalculateElementsToShow() {
    // Needed to prevent racing conditions.
    this.currentPage = this.currentPageInUrl;

    // Needed to prevent racing conditions.
    if (this.filteredPorts) {
      // Calculate the pagination values.
      const maxElements = this.showShortList_ ? AppConfig.maxShortListElements : AppConfig.maxFullListElements;
      this.numberOfPages = Math.ceil(this.filteredPorts.length / maxElements);
      if (this.currentPage > this.numberOfPages) {
        this.currentPage = this.numberOfPages;
      }

      // Limit the elements to show.
      const start = maxElements * (this.currentPage - 1);
      const end = start + maxElements;
      this.PortsToShow = this.filteredPorts.slice(start, end);

      // Create a map with the elements to show, as a helper.
      const currentElementsMap = new Map<string, boolean>();
      this.PortsToShow.forEach(port => {
        currentElementsMap.set(port.connectionID, true);

        // Add to the selections map the elements that are going to be shown.
        if (!this.selections.has(port.connectionID)) {
          this.selections.set(port.connectionID, false);
        }
      });

      // Remove from the selections map the elements that are not going to be shown.
      const keysToRemove: string[] = [];
      this.selections.forEach((value, key) => {
        if (!currentElementsMap.has(key)) {
          keysToRemove.push(key);
        }
      });
      keysToRemove.forEach(key => {
        this.selections.delete(key);
      });

    } else {
      this.PortsToShow = null;
      this.selections = new Map<string, boolean>();
    }

    this.dataSource = this.PortsToShow;
  }

  /**
   * Prepares the operation for deteling an element, but does not start it. To start the operation,
   * subscribe to the response.
   */
  private startDeleting(connectionID: string): Observable<any> {
    return this.fwdService.deleteRemote(NodeComponent.getCurrentNodeKey(), connectionID);
  }

  /**
   * Recursively deletes a list of elements.
   * @param ids List with the IDs of the elements to delete.
   * @param confirmationDialog Dialog used for requesting confirmation from the user.
   */
  deleteRecursively(ids: string[], confirmationDialog: MatDialogRef<ConfirmationComponent, any>) {
    this.operationSubscriptionsGroup.push(this.startDeleting(ids[ids.length - 1]).subscribe(() => {
      ids.pop();
      if (ids.length === 0) {
        confirmationDialog.close();
        // Make the parent page reload the data.
        NodeComponent.refreshCurrentDisplayedData();
        this.snackbarService.showDone('remote-rev-ports.deleted');
      } else {
        this.deleteRecursively(ids, confirmationDialog);
      }
    }, (err: OperationError) => {
      NodeComponent.refreshCurrentDisplayedData();

      err = processServiceError(err);
      confirmationDialog.componentInstance.showDone('confirmation.error-header-text', err.translatableErrorMsg);
    }));
  }
}
