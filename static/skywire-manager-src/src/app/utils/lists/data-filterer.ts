import { MatDialog } from '@angular/material/dialog';
import { Subject, Observable, Subscription } from 'rxjs';
import { ActivatedRoute, Router } from '@angular/router';

import { FilterTextElements, FilterProperties, CompleteFilterProperties, updateFilterTexts, filterList } from '../filters';
import GeneralUtils from '../generalUtils';
import {
  FiltersSelectionComponent,
  FiltersSelectiondParams
} from 'src/app/components/layout/filters-selection/filters-selection.component';

/**
 * Helper for filtering the data shown by an UI list. It allows to open the modal window used
 * for entering the filters.
 */
export class DataFilterer {
  // Array with data about each filter.
  private filterPropertiesList: CompleteFilterProperties[];
  // Current filters for the data.
  private currentFilters: object;
  // Properties needed for showing the selected filters in the UI.
  private currentFiltersTextsInternal: FilterTextElements[] = [];
  get currentFiltersTexts(): FilterTextElements[] { return this.currentFiltersTextsInternal; }
  // Current params in the query string added to the url.
  private currentUrlQueryParamsInternal: object;
  get currentUrlQueryParams(): object { return this.currentUrlQueryParamsInternal; }
  // Data to filter.
  private data: any[];

  private dataUpdatedSubject = new Subject<any[]>();

  private navigationsSubscription: Subscription;

  /**
   * Emits every time the data is filtered. It returns the filtered array.
   */
  get dataFiltered(): Observable<any[]> {
    return this.dataUpdatedSubject.asObservable();
  }

  /**
   * @param filterPropertiesList List with the data for each filter.
   * @param id Unique short text identifying the list.
   */
  constructor(
    private dialog: MatDialog,
    private route: ActivatedRoute,
    private router: Router,
    filterPropertiesList: FilterProperties[],
    id: string,
  ) {
    this.filterPropertiesList = filterPropertiesList as CompleteFilterProperties[];

    // Create the object that will contain the current filters and add to each filter data which
    // indicates that no filter has been selected (empty string). Also, add to
    // filterPropertiesList the properties needed for converting it from
    // FilterProperties[] to CompleteFilterProperties[].
    this.currentFilters = {};
    this.filterPropertiesList.forEach(property => {
      property.keyNameInFiltersObject = id + '_' + property.keyNameInElementsArray;
      this.currentFilters[property.keyNameInFiltersObject] = '';
    });

    // Get the query string.
    this.navigationsSubscription = this.route.queryParamMap.subscribe(queryParams => {
      // Get the filters from the query string.
      Object.keys(this.currentFilters).forEach(key => {
        if (queryParams.has(key)) {
          this.currentFilters[key] = queryParams.get(key);
        }
      });

      // Save the query string.
      this.currentUrlQueryParamsInternal = {};
      queryParams.keys.forEach(key => {
        this.currentUrlQueryParamsInternal[key] = queryParams.get(key);
      });

      // Update the filtered data.
      this.filter();
    });
  }

  /**
   * Must be called after finishing using the instance.
   */
  dispose() {
    this.dataUpdatedSubject.complete();
    this.navigationsSubscription.unsubscribe();
  }

  /**
   * Sets the data and inmediatelly filters it. The result is returned in an event and the
   * original provided list is not modified.
   * @param data Data to filter.
   */
  setData(data: any[]) {
    this.data = data;
    this.filter();
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
    const filtersSelectiondParams: FiltersSelectiondParams = {
      filterPropertiesList: this.filterPropertiesList,
      currentFilters: this.currentFilters,
    };

    // Open the modal window.
    FiltersSelectionComponent.openDialog(this.dialog, filtersSelectiondParams).afterClosed().subscribe(response => {
      if (response) {
        this.router.navigate([], { queryParams: response});
      }
    });
  }

  /**
   * Filters the data.
   */
  private filter() {
    if (this.data) {
      let filteredData: any[];

      // Check if at least one filter is valid.
      let filtersSet = false;
      Object.keys(this.currentFilters).forEach(key => {
        if (this.currentFilters[key]) {
          filtersSet = true;
        }
      });

      if (filtersSet) {
        filteredData = filterList(this.data, this.currentFilters, this.filterPropertiesList);

        this.updateCurrentFilters();
      } else {
        filteredData = this.data;

        this.updateCurrentFilters();
      }

      // Return the filtered data.
      this.dataUpdatedSubject.next(filteredData);
    }
  }

  /**
   * Updates the texts with the currently selected filters.
   */
  private updateCurrentFilters() {
    this.currentFiltersTextsInternal = updateFilterTexts(this.currentFilters, this.filterPropertiesList);
  }
}
