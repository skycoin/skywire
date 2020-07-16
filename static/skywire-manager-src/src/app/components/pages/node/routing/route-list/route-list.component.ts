import { Component, Input, OnDestroy } from '@angular/core';
import { MatDialog, MatDialogRef } from '@angular/material/dialog';
import { ActivatedRoute, Router } from '@angular/router';
import { Observable, Subscription } from 'rxjs';

import { Route } from 'src/app/app.datatypes';
import { RouteService } from '../../../../../services/route.service';
import { NodeComponent } from '../../node.component';
import { RouteDetailsComponent } from './route-details/route-details.component';
import { AppConfig } from '../../../../../app.config';
import GeneralUtils from '../../../../../utils/generalUtils';
import { ConfirmationComponent } from '../../../../layout/confirmation/confirmation.component';
import { SnackbarService } from '../../../../../services/snackbar.service';
import { SelectOptionComponent, SelectableOption } from 'src/app/components/layout/select-option/select-option.component';
import { OperationError } from 'src/app/utils/operation-error';
import { processServiceError } from 'src/app/utils/errors';
import { TranslateService } from '@ngx-translate/core';
import { FilterKeysAssociation, FilterTextElements, filterList, updateFilterTexts } from 'src/app/utils/filters';
import {
  FilterFieldParams,
  FilterFieldTypes,
  FiltersSelectionComponent
} from 'src/app/components/layout/filters-selection/filters-selection.component';

/**
 * List of the columns that can be used to sort the data.
 */
enum SortableColumns {
  Key = 'routes.key',
  Rule = 'routes.rule',
}

/**
 * Filters for the list. It is prepopulated with default data which indicates that no filter
 * has been selected. As the object may be included in the query string, prefixes are used to
 * avoid name collisions with other components in the same URL.
 */
class DataFilters {
  rt_key = '';
  rt_rule = '';
}

/**
 * Shows the list of routes of a node. I can be used to show a short preview, with just some
 * elements and a link for showing the rest: or the full list, with pagination controls.
 */
@Component({
  selector: 'app-route-list',
  templateUrl: './route-list.component.html',
  styleUrls: ['./route-list.component.scss']
})
export class RouteListComponent implements OnDestroy {
  private static sortByInternal = SortableColumns.Key;
  private static sortReverseInternal = false;

  @Input() nodePK: string;

  // Vars for keeping track of the column used for sorting the data.
  sortableColumns = SortableColumns;
  get sortBy(): SortableColumns { return RouteListComponent.sortByInternal; }
  set sortBy(val: SortableColumns) { RouteListComponent.sortByInternal = val; }
  get sortReverse(): boolean { return RouteListComponent.sortReverseInternal; }
  set sortReverse(val: boolean) { RouteListComponent.sortReverseInternal = val; }
  get sortingArrow(): string {
    return this.sortReverse ? 'keyboard_arrow_up' : 'keyboard_arrow_down';
  }

  dataSource: Route[];
  /**
   * Keeps track of the state of the check boxes of the elements.
   */
  selections = new Map<number, boolean>();

  /**
   * If true, the control can only show few elements and, if there are more elements, a link for
   * accessing the full list. If false, the full list is shown, with pagination
   * controls, if needed.
   */
  showShortList_: boolean;
  @Input() set showShortList(val: boolean) {
    this.showShortList_ = val;
    this.recalculateElementsToShow();
  }

  allRoutes: Route[];
  filteredRoutes: Route[];
  routesToShow: Route[];
  numberOfPages = 1;
  currentPage = 1;
  // Used as a helper var, as the URL is read asynchronously.
  currentPageInUrl = 1;
  @Input() set routes(val: Route[]) {
    this.allRoutes = val;
    this.filter();
  }

  // Array allowing to associate the properties of TransportFilters with the ones on the list
  // and the values that must be shown in the UI, for being able to use helper functions to
  // filter the data and show some UI elements.
  filterKeysAssociations: FilterKeysAssociation[] = [
    {
      filterName: 'routes.filter-dialog.key',
      keyNameInElementsArray: 'key',
      keyNameInFiltersObject: 'rt_key',
    },
    {
      filterName: 'routes.filter-dialog.rule',
      keyNameInElementsArray: 'rule',
      keyNameInFiltersObject: 'rt_rule',
    }
  ];

  // Current filters for the data.
  currentFilters = new DataFilters();
  // Properties needed for showing the selected filters in the UI.
  currentFiltersTexts: FilterTextElements[] = [];
  // Current params in the query string added to the url.
  currentUrlQueryParams: object;

  private navigationsSubscription: Subscription;
  private operationSubscriptionsGroup: Subscription[] = [];

  constructor(
    private routeService: RouteService,
    private dialog: MatDialog,
    private route: ActivatedRoute,
    private router: Router,
    private snackbarService: SnackbarService,
    private translateService: TranslateService,
  ) {
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
  }

  /**
   * Changes the selection state of an entry (modifies the state of its checkbox).
   */
  changeSelection(route: Route) {
    if (this.selections.get(route.key)) {
      this.selections.set(route.key, false);
    } else {
      this.selections.set(route.key, true);
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
    const confirmationDialog = GeneralUtils.createConfirmationDialog(this.dialog, 'routes.delete-selected-confirmation');

    confirmationDialog.componentInstance.operationAccepted.subscribe(() => {
      confirmationDialog.componentInstance.showProcessing();

      const elementsToRemove: number[] = [];
      this.selections.forEach((val, key) => {
        if (val) {
          elementsToRemove.push(key);
        }
      });

      this.deleteRecursively(elementsToRemove, confirmationDialog);
    });
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
      type: FilterFieldTypes.TextInput,
      currentValue: this.currentFilters.rt_key,
      filterKeysAssociation: this.filterKeysAssociations[0],
      maxlength: 36,
    });
    filterFieldsParams.push({
      type: FilterFieldTypes.TextInput,
      currentValue: this.currentFilters.rt_rule,
      filterKeysAssociation: this.filterKeysAssociations[1],
      maxlength: 100,
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
    if (this.allRoutes) {
      this.filteredRoutes = filterList(this.allRoutes, this.currentFilters, this.filterKeysAssociations);

      this.updateCurrentFilters();
      this.recalculateElementsToShow();
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
  showOptionsDialog(route: Route) {
    const options: SelectableOption[] = [
      {
        icon: 'visibility',
        label: 'routes.details.title',
      },
      {
        icon: 'close',
        label: 'routes.delete',
      }
    ];

    SelectOptionComponent.openDialog(this.dialog, options, 'common.options').afterClosed().subscribe((selectedOption: number) => {
      if (selectedOption === 1) {
        this.details(route.key.toString());
      } else if (selectedOption === 2) {
        this.delete(route.key);
      }
    });
  }

  /**
   * Shows a modal window with the details of a route.
   */
  details(route: string) {
    RouteDetailsComponent.openDialog(this.dialog, route);
  }

  /**
   * Deletes a specific element.
   */
  delete(routeKey: number) {
    const confirmationDialog = GeneralUtils.createConfirmationDialog(this.dialog, 'routes.delete-confirmation');

    confirmationDialog.componentInstance.operationAccepted.subscribe(() => {
      confirmationDialog.componentInstance.showProcessing();

      // Start the operation and save it for posible cancellation.
      this.operationSubscriptionsGroup.push(this.startDeleting(routeKey).subscribe(() => {
        confirmationDialog.close();
        // Make the parent page reload the data.
        NodeComponent.refreshCurrentDisplayedData();
        this.snackbarService.showDone('routes.deleted');
      }, (err: OperationError) => {
        err = processServiceError(err);
        confirmationDialog.componentInstance.showDone('confirmation.error-header-text', err.translatableErrorMsg);
      }));
    });
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

    this.recalculateElementsToShow();
  }

  /**
   * Opens the modal window used on small screens for selecting how to sort the data.
   */
  openSortingOrderModal() {
    // Create 2 options for every sortable column, for ascending and descending order.
    const options: SelectableOption[] = [];
    const enumKeys = Object.keys(SortableColumns);
    enumKeys.forEach(key => {
      options.push({
        label: this.translateService.instant(SortableColumns[key]) + ' ' + this.translateService.instant('tables.ascending-order'),
      });
      options.push({
        label: this.translateService.instant(SortableColumns[key]) + ' ' + this.translateService.instant('tables.descending-order'),
      });
    });

    // Open the option selection modal window.
    SelectOptionComponent.openDialog(this.dialog, options, 'tables.title').afterClosed().subscribe((result: number) => {
      if (result) {
        result = (result - 1) / 2;
        const index = Math.floor(result);
        // Use the column and order selected by the user.
        this.sortBy = SortableColumns[enumKeys[index]];
        this.sortReverse = result !== index;

        this.recalculateElementsToShow();
      }
    });
  }

  /**
   * Sorts the data and recalculates which elements should be shown on the UI.
   */
  private recalculateElementsToShow() {
    // Needed to prevent racing conditions.
    this.currentPage = this.currentPageInUrl;

    // Needed to prevent racing conditions.
    if (this.filteredRoutes) {
      // Sort all the data.
      this.filteredRoutes.sort((a, b) => {
        const defaultOrder = a.key - b.key;

        let response: number;
        if (this.sortBy === SortableColumns.Key) {
          response = !this.sortReverse ? a.key - b.key : b.key - a.key;
        } else if (this.sortBy === SortableColumns.Rule) {
          response = !this.sortReverse ? a.rule.localeCompare(b.rule) : b.rule.localeCompare(a.rule);
        } else {
          response = defaultOrder;
        }

        return response !== 0 ? response : defaultOrder;
      });

      // Calculate the pagination values.
      const maxElements = this.showShortList_ ? AppConfig.maxShortListElements : AppConfig.maxFullListElements;
      this.numberOfPages = Math.ceil(this.filteredRoutes.length / maxElements);
      if (this.currentPage > this.numberOfPages) {
        this.currentPage = this.numberOfPages;
      }

      // Limit the elements to show.
      const start = maxElements * (this.currentPage - 1);
      const end = start + maxElements;
      this.routesToShow = this.filteredRoutes.slice(start, end);

      // Create a map with the elements to show, as a helper.
      const currentElementsMap = new Map<number, boolean>();
      this.routesToShow.forEach(route => {
        currentElementsMap.set(route.key, true);

        // Add to the selections map the elements that are going to be shown.
        if (!this.selections.has(route.key)) {
          this.selections.set(route.key, false);
        }
      });

      // Remove from the selections map the elements that are not going to be shown.
      const keysToRemove: number[] = [];
      this.selections.forEach((value, key) => {
        if (!currentElementsMap.has(key)) {
          keysToRemove.push(key);
        }
      });
      keysToRemove.forEach(key => {
        this.selections.delete(key);
      });
    } else {
      this.routesToShow = null;
      this.selections = new Map<number, boolean>();
    }

    this.dataSource = this.routesToShow;
  }

  /**
   * Prepares the operation for deteling an element, but does not start it. To start the operation,
   * subscribe to the response.
   */
  private startDeleting(routeKey: number): Observable<any> {
    return this.routeService.delete(NodeComponent.getCurrentNodeKey(), routeKey.toString());
  }

  /**
   * Recursively deletes a list of elements.
   * @param ids List with the IDs of the elements to delete.
   * @param confirmationDialog Dialog used for requesting confirmation from the user.
   */
  deleteRecursively(ids: number[], confirmationDialog: MatDialogRef<ConfirmationComponent, any>) {
    this.operationSubscriptionsGroup.push(this.startDeleting(ids[ids.length - 1]).subscribe(() => {
      ids.pop();
      if (ids.length === 0) {
        confirmationDialog.close();
        // Make the parent page reload the data.
        NodeComponent.refreshCurrentDisplayedData();
        this.snackbarService.showDone('routes.deleted');
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
