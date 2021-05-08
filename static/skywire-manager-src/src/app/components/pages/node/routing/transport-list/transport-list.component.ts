import { Component, Input, OnDestroy } from '@angular/core';
import { MatDialog, MatDialogRef } from '@angular/material/dialog';
import { Observable, Subscription, forkJoin } from 'rxjs';
import { ActivatedRoute, Router } from '@angular/router';
import { TranslateService } from '@ngx-translate/core';

import { Transport } from '../../../../../app.datatypes';
import { CreateTransportComponent } from './create-transport/create-transport.component';
import { TransportService } from '../../../../../services/transport.service';
import { NodeComponent } from '../../node.component';
import { AppConfig } from '../../../../../app.config';
import { ConfirmationComponent } from '../../../../layout/confirmation/confirmation.component';
import GeneralUtils from '../../../../../utils/generalUtils';
import { TransportDetailsComponent } from './transport-details/transport-details.component';
import { SnackbarService } from '../../../../../services/snackbar.service';
import { SelectableOption, SelectOptionComponent } from 'src/app/components/layout/select-option/select-option.component';
import { processServiceError } from 'src/app/utils/errors';
import { OperationError } from 'src/app/utils/operation-error';
import { FilterProperties, FilterFieldTypes } from 'src/app/utils/filters';
import { LabeledElementTypes, StorageService } from 'src/app/services/storage.service';
import { LabeledElementTextComponent } from 'src/app/components/layout/labeled-element-text/labeled-element-text.component';
import { DataSorter, SortingColumn, SortingModes } from 'src/app/utils/lists/data-sorter';
import { DataFilterer } from 'src/app/utils/lists/data-filterer';

/**
 * Shows the list of transports of a node. I can be used to show a short preview, with just some
 * elements and a link for showing the rest: or the full list, with pagination controls.
 */
@Component({
  selector: 'app-transport-list',
  templateUrl: './transport-list.component.html',
  styleUrls: ['./transport-list.component.scss']
})
export class TransportListComponent implements OnDestroy {
  // Small text for identifying the list, needed for the helper objects.
  private readonly listId = 'tr';

  @Input() nodePK: string;

  // Vars with the data of the columns used for sorting the data.
  stateSortData = new SortingColumn(['isUp'], 'transports.state', SortingModes.Boolean);
  idSortData = new SortingColumn(['id'], 'transports.id', SortingModes.Text, ['id_label']);
  remotePkSortData = new SortingColumn(['remotePk'], 'transports.remote-node', SortingModes.Text, ['remote_pk_label']);
  typeSortData = new SortingColumn(['type'], 'transports.type', SortingModes.Text);
  uploadedSortData = new SortingColumn(['sent'], 'common.uploaded', SortingModes.NumberReversed);
  downloadedSortData = new SortingColumn(['recv'], 'common.downloaded', SortingModes.NumberReversed);

  private dataSortedSubscription: Subscription;
  private dataFiltererSubscription: Subscription;
  // Objects in charge of sorting and filtering the data.
  dataSorter: DataSorter;
  dataFilterer: DataFilterer;

  dataSource: Transport[];
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
    this.dataSorter.setData(this.filteredTransports);
  }

  allTransports: Transport[];
  filteredTransports: Transport[];
  transportsToShow: Transport[];
  hasOfflineTransports = false;
  numberOfPages = 1;
  currentPage = 1;
  // Used as a helper var, as the URL is read asynchronously.
  currentPageInUrl = 1;
  @Input() set transports(val: Transport[]) {
    this.allTransports = val;

    // Add the label data to the array, to be able to use it for filtering and sorting.
    this.allTransports.forEach(transport => {
      transport['id_label'] =
        LabeledElementTextComponent.getCompleteLabel(this.storageService, this.translateService, transport.id);

      transport['remote_pk_label'] =
        LabeledElementTextComponent.getCompleteLabel(this.storageService, this.translateService, transport.remotePk);
    });

    this.dataFilterer.setData(this.allTransports);
  }

  // Array with the properties of the columns that can be used for filtering the data.
  filterProperties: FilterProperties[] = [
    {
      filterName: 'transports.filter-dialog.online',
      keyNameInElementsArray: 'isUp',
      type: FilterFieldTypes.Select,
      printableLabelsForValues: [
        {
          value: '',
          label: 'transports.filter-dialog.online-options.any',
        },
        {
          value: 'true',
          label: 'transports.filter-dialog.online-options.online',
        },
        {
          value: 'false',
          label: 'transports.filter-dialog.online-options.offline',
        }
      ],
    },
    {
      filterName: 'transports.filter-dialog.id',
      keyNameInElementsArray: 'id',
      secondaryKeyNameInElementsArray: 'id_label',
      type: FilterFieldTypes.TextInput,
      maxlength: 36,
    },
    {
      filterName: 'transports.filter-dialog.remote-node',
      keyNameInElementsArray: 'remotePk',
      secondaryKeyNameInElementsArray: 'remote_pk_label',
      type: FilterFieldTypes.TextInput,
      maxlength: 66,
    }
  ];

  labeledElementTypes = LabeledElementTypes;

  private navigationsSubscription: Subscription;
  private operationSubscriptionsGroup: Subscription[] = [];
  private languageSubscription: Subscription;

  constructor(
    private dialog: MatDialog,
    private transportService: TransportService,
    private route: ActivatedRoute,
    private router: Router,
    private snackbarService: SnackbarService,
    private translateService: TranslateService,
    private storageService: StorageService,
  ) {
    // Initialize the data sorter.
    const sortableColumns: SortingColumn[] = [
      this.stateSortData,
      this.idSortData,
      this.remotePkSortData,
      this.typeSortData,
      this.uploadedSortData,
      this.downloadedSortData,
    ];
    this.dataSorter = new DataSorter(this.dialog, this.translateService, sortableColumns, 1, this.listId);
    this.dataSortedSubscription = this.dataSorter.dataSorted.subscribe(() => {
      // When this happens, the data in allTransports has already been sorted.
      this.recalculateElementsToShow();
    });

    this.dataFilterer = new DataFilterer(this.dialog, this.route, this.router, this.filterProperties, this.listId);
    this.dataFiltererSubscription = this.dataFilterer.dataFiltered.subscribe(data => {
      this.filteredTransports = data;

      // Check if there are offline transports.
      this.hasOfflineTransports = false;
      this.filteredTransports.forEach(transport => {
        if (!transport.isUp) {
          this.hasOfflineTransports = true;
        }
      });

      this.dataSorter.setData(this.filteredTransports);
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
      this.transports = this.allTransports;
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
   * Returns the scss class to be used to show the current status of the transport.
   * @param forDot If true, returns a class for creating a colored dot. If false,
   * returns a class for a colored text.
   */
  transportStatusClass(transport: Transport, forDot: boolean): string {
    switch (transport.isUp) {
      case true:
        return forDot ? 'dot-green' : 'green-clear-text';
      default:
        return forDot ? 'dot-red' : 'red-clear-text';
    }
  }

  /**
   * Returns the text to be used to indicate the current status of a transport.
   * @param forTooltip If true, returns a text for a tooltip. If false, returns a
   * text for the transport list shown on small screens.
   */
  transportStatusText(transport: Transport, forTooltip: boolean): string {
    switch (transport.isUp) {
      case true:
        return 'transports.statuses.online' + (forTooltip ? '-tooltip' : '');
      default:
        return 'transports.statuses.offline' + (forTooltip ? '-tooltip' : '');
    }
  }

  /**
   * Changes the selection state of an entry (modifies the state of its checkbox).
   */
  changeSelection(transport: Transport) {
    if (this.selections.get(transport.id)) {
      this.selections.set(transport.id, false);
    } else {
      this.selections.set(transport.id, true);
    }
  }

  /**
   * Check if at lest one entry has been selected via its checkbox.
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
    const confirmationDialog = GeneralUtils.createConfirmationDialog(this.dialog, 'transports.delete-selected-confirmation');

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
   * Removes all offline transports.
   */
  removeOffline() {
    let confirmationText = 'transports.remove-all-offline-confirmation';
    if (this.dataFilterer.currentFiltersTexts && this.dataFilterer.currentFiltersTexts.length > 0) {
      confirmationText = 'transports.remove-all-filtered-offline-confirmation';
    }

    const confirmationDialog = GeneralUtils.createConfirmationDialog(this.dialog, confirmationText);

    confirmationDialog.componentInstance.operationAccepted.subscribe(() => {
      // Prepare all offline transports to be removed.
      const transportsToRemove: string[] = [];
      this.filteredTransports.forEach(transport => {
        if (!transport.isUp) {
          transportsToRemove.push(transport.id);
        }
      });

      if (transportsToRemove.length > 0) {
        // Remove the transports.
        confirmationDialog.componentInstance.showProcessing();
        this.deleteRecursively(transportsToRemove, confirmationDialog);
      } else {
        confirmationDialog.close();
      }
    });
  }

  /**
   * Shows the transport creation modal window.
   */
  create() {
    CreateTransportComponent.openDialog(this.dialog);
  }

  /**
   * Opens the modal window used on small screens with the options of an element.
   */
  showOptionsDialog(transport: Transport) {
    const options: SelectableOption[] = [
      {
        icon: 'visibility',
        label: 'transports.details.title',
      },
      {
        icon: 'close',
        label: 'transports.delete',
      }
    ];

    SelectOptionComponent.openDialog(this.dialog, options, 'common.options').afterClosed().subscribe((selectedOption: number) => {
      if (selectedOption === 1) {
        this.details(transport);
      } else if (selectedOption === 2) {
        this.delete(transport.id);
      }
    });
  }

  /**
   * Shows a modal window with the details of a transport.
   */
  details(transport: Transport) {
    TransportDetailsComponent.openDialog(this.dialog, transport);
  }

  /**
   * Deletes a specific element.
   */
  delete(id: string) {
    const confirmationDialog = GeneralUtils.createConfirmationDialog(this.dialog, 'transports.delete-confirmation');

    confirmationDialog.componentInstance.operationAccepted.subscribe(() => {
      confirmationDialog.componentInstance.showProcessing();

      // Start the operation and save it for posible cancellation.
      this.operationSubscriptionsGroup.push(this.startDeleting(id).subscribe(() => {
        confirmationDialog.close();
        // Make the parent page reload the data.
        NodeComponent.refreshCurrentDisplayedData();
        this.snackbarService.showDone('transports.deleted');
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
    if (this.filteredTransports) {
      // Calculate the pagination values.
      const maxElements = this.showShortList_ ? AppConfig.maxShortListElements : AppConfig.maxFullListElements;
      this.numberOfPages = Math.ceil(this.filteredTransports.length / maxElements);
      if (this.currentPage > this.numberOfPages) {
        this.currentPage = this.numberOfPages;
      }

      // Limit the elements to show.
      const start = maxElements * (this.currentPage - 1);
      const end = start + maxElements;
      this.transportsToShow = this.filteredTransports.slice(start, end);

      // Create a map with the elements to show, as a helper.
      const currentElementsMap = new Map<string, boolean>();
      this.transportsToShow.forEach(transport => {
        currentElementsMap.set(transport.id, true);

        // Add to the selections map the elements that are going to be shown.
        if (!this.selections.has(transport.id)) {
          this.selections.set(transport.id, false);
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
      this.transportsToShow = null;
      this.selections = new Map<string, boolean>();
    }

    this.dataSource = this.transportsToShow;
  }

  /**
   * Prepares the operation for deteling an element, but does not start it. To start the operation,
   * subscribe to the response.
   */
  private startDeleting(id: string): Observable<any> {
    return this.transportService.delete(NodeComponent.getCurrentNodeKey(), id);
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
        this.snackbarService.showDone('transports.deleted');
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
