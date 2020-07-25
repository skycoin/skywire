import { Component, Input, OnDestroy } from '@angular/core';
import { MatDialog, MatDialogRef } from '@angular/material/dialog';
import { Observable, Subscription } from 'rxjs';
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
import { FilterTextElements, FilterKeysAssociation, filterList, updateFilterTexts } from 'src/app/utils/filters';
import {
  FilterFieldParams,
  FiltersSelectionComponent,
  FilterFieldTypes
} from 'src/app/components/layout/filters-selection/filters-selection.component';
import { LabeledElementTypes, StorageService } from 'src/app/services/storage.service';
import { LabeledElementTextComponent } from 'src/app/components/layout/labeled-element-text/labeled-element-text.component';
import { DataSorter, SortingColumn, SortingModes } from 'src/app/utils/lists/data-sorter';

/**
 * Filters for the list. It is prepopulated with default data which indicates that no filter
 * has been selected. As the object may be included in the query string, prefixes are used to
 * avoid name collisions with other components in the same URL.
 */
class DataFilters {
  tr_online = '';
  tr_id = '';
  tr_id_label = '';
  tr_key = '';
  tr_key_label = '';
}

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
  @Input() nodePK: string;

  // Vars with the data of the columns used for sorting the data.
  stateSortData = new SortingColumn(['is_up'], 'transports.state', SortingModes.Boolean);
  idSortData = new SortingColumn(['id'], 'transports.id', SortingModes.Text);
  remotePkSortData = new SortingColumn(['remote_pk'], 'transports.remote-node', SortingModes.Text);
  typeSortData = new SortingColumn(['type'], 'transports.type', SortingModes.Text);
  uploadedSortData = new SortingColumn(['log', 'sent'], 'common.uploaded', SortingModes.NumberReversed);
  downloadedSortData = new SortingColumn(['log', 'recv'], 'common.downloaded', SortingModes.NumberReversed);

  private dataSortedSubscription: Subscription;
  // Object in chage of sorting the data.
  dataSorter: DataSorter;

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
  numberOfPages = 1;
  currentPage = 1;
  // Used as a helper var, as the URL is read asynchronously.
  currentPageInUrl = 1;
  @Input() set transports(val: Transport[]) {
    this.allTransports = val;
    this.filter();
  }

  // Array allowing to associate the properties of TransportFilters with the ones on the list
  // and the values that must be shown in the UI, for being able to use helper functions to
  // filter the data and show some UI elements.
  filterKeysAssociations: FilterKeysAssociation[] = [
    {
      filterName: 'transports.filter-dialog.online',
      keyNameInElementsArray: 'is_up',
      keyNameInFiltersObject: 'tr_online',
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
      keyNameInFiltersObject: 'tr_id',
    },
    {
      filterName: 'transports.filter-dialog.remote-node',
      keyNameInElementsArray: 'remote_pk',
      secondaryKeyNameInElementsArray: 'remote_pk_label',
      keyNameInFiltersObject: 'tr_key',
    }
  ];

  // Current filters for the data.
  currentFilters = new DataFilters();
  // Properties needed for showing the selected filters in the UI.
  currentFiltersTexts: FilterTextElements[] = [];
  // Current params in the query string added to the url.
  currentUrlQueryParams: object;

  labeledElementTypes = LabeledElementTypes;

  private navigationsSubscription: Subscription;
  private operationSubscriptionsGroup: Subscription[] = [];

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
    this.dataSorter = new DataSorter(this.dialog, this.translateService, sortableColumns, 1, 'tr');
    this.dataSortedSubscription = this.dataSorter.dataSorted.subscribe(() => {
      // When this happens, the data in allTransports has already been sorted.
      this.recalculateElementsToShow();
    });

    // Get the page requested in the URL.
    this.navigationsSubscription = this.route.paramMap.subscribe(params => {
      if (params.has('page')) {
        let selectedPage = Number.parseInt(params.get('page'), 10);
        if (isNaN(selectedPage) || selectedPage < 1) {
          selectedPage = 1;
        }

        this.currentPageInUrl = selectedPage;

        this.filter();
      }
    });

    // Get the query string.
    this.navigationsSubscription.add(this.route.queryParamMap.subscribe(queryParams => {
      // Get the filters from the query string.
      this.currentFilters = new DataFilters();
      Object.keys(this.currentFilters).forEach(key => {
        if (queryParams.has(key)) {
          this.currentFilters[key] = queryParams.get(key);
        }
      });

      // Save the query string.
      this.currentUrlQueryParams = {};
      queryParams.keys.forEach(key => {
        this.currentUrlQueryParams[key] = queryParams.get(key);
      });

      this.filter();
    }));
  }

  ngOnDestroy() {
    this.navigationsSubscription.unsubscribe();
    this.operationSubscriptionsGroup.forEach(sub => sub.unsubscribe());
    this.dataSortedSubscription.unsubscribe();
    this.dataSorter.dispose();
  }

  /**
   * Returns the scss class to be used to show the current status of the transport.
   * @param forDot If true, returns a class for creating a colored dot. If false,
   * returns a class for a colored text.
   */
  transportStatusClass(transport: Transport, forDot: boolean): string {
    switch (transport.is_up) {
      case true:
        return forDot ? 'dot-green' : 'green-text';
      default:
        return forDot ? 'dot-red' : 'red-text';
    }
  }

  /**
   * Returns the text to be used to indicate the current status of a transport.
   * @param forTooltip If true, returns a text for a tooltip. If false, returns a
   * text for the transport list shown on small screens.
   */
  transportStatusText(transport: Transport, forTooltip: boolean): string {
    switch (transport.is_up) {
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
   * Shows the transport creation modal window.
   */
  create() {
    CreateTransportComponent.openDialog(this.dialog);
  }

  /**
   * Removes all the filters added by the user.
   */
  removeFilters() {
    const confirmationDialog = GeneralUtils.createConfirmationDialog(this.dialog, 'filters.remove-confirmation');

    // Ask for confirmation.
    confirmationDialog.componentInstance.operationAccepted.subscribe(() => {
      confirmationDialog.componentInstance.closeModal();

      // Remove the query string params.
      this.router.navigate([], { queryParams: {}});
    });
  }

  /**
   * Opens the filter selection modal window to let the user change the currently selected filters.
   */
  changeFilters() {
    // Properties for the modal window.
    const filterFieldsParams: FilterFieldParams[] = [];
    filterFieldsParams.push({
      type: FilterFieldTypes.Select,
      currentValue: this.currentFilters.tr_online,
      filterKeysAssociation: this.filterKeysAssociations[0]
    });
    filterFieldsParams.push({
      type: FilterFieldTypes.TextInput,
      currentValue: this.currentFilters.tr_id,
      filterKeysAssociation: this.filterKeysAssociations[1],
      maxlength: 36,
    });
    filterFieldsParams.push({
      type: FilterFieldTypes.TextInput,
      currentValue: this.currentFilters.tr_key,
      filterKeysAssociation: this.filterKeysAssociations[2],
      maxlength: 66,
    });

    // Open the modal window.
    FiltersSelectionComponent.openDialog(this.dialog, filterFieldsParams).afterClosed().subscribe(response => {
      if (response) {
        this.router.navigate([], { queryParams: response});
      }
    });
  }

  /**
   * Filters the data, saves the filtered list in the corresponding array and updates the UI.
   */
  private filter() {
    if (this.allTransports) {
      // Check if at least one filter is valid.
      let filtersSet = false;
      Object.keys(this.currentFilters).forEach(key => {
        if (this.currentFilters[key]) {
          filtersSet = true;
        }
      });

      if (filtersSet) {
        // Add the label data to the array, to be able to use it for filtering.
        this.allTransports.forEach(transport => {
          transport['id_label'] =
            LabeledElementTextComponent.getCompleteLabel(this.storageService, this.translateService, transport.id);

          transport['remote_pk_label'] =
            LabeledElementTextComponent.getCompleteLabel(this.storageService, this.translateService, transport.remote_pk);
        });

        this.filteredTransports = filterList(this.allTransports, this.currentFilters, this.filterKeysAssociations);

        this.updateCurrentFilters();
      } else {
        this.filteredTransports = this.allTransports;
      }

      this.dataSorter.setData(this.filteredTransports);
    }
  }

  /**
   * Updates the texts with the currently selected filters.
   */
  private updateCurrentFilters() {
    this.currentFiltersTexts = updateFilterTexts(this.currentFilters, this.filterKeysAssociations);
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
   * Sorts the data and recalculates which elements should be shown on the UI.
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
