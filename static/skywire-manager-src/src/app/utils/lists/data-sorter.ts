import { MatDialog } from '@angular/material/dialog';
import { TranslateService } from '@ngx-translate/core';
import { Subject, Observable } from 'rxjs';

import { SelectableOption, SelectOptionComponent } from 'src/app/components/layout/select-option/select-option.component';

/**
 * Data about a column that can be used to sort the data of a list.
 */
export class SortingColumn {
  /**
   * Name of the property on the data array shown in the column. As the property may be nested
   * inside another properties, you can enter the property path in this array. Example: if the
   * data shown in the column is in the "userDetails.idData.name" property, this array must be
   * ['userDetails', 'idData', 'name'].
   */
  properties: string[];
  /**
   * Translatable var with the name of the column.
   */
  label: string;
  /**
   * How the data must be sorted.
   */
  sortingMode: SortingModes;

  constructor(properties: string[], label: string, sortingMode: SortingModes) {
    this.properties = properties;
    this.label = label;
    this.sortingMode = sortingMode;
  }

  /**
   * Returns a string to be used as ID for the column. It is just a concatenation of the
   * "properties" array.
   */
  get id(): string {
    return this.properties.join('');
  }
}

/**
 * Modes for sorting the data.
 */
export enum SortingModes {
  /**
   * Sorts the data alphabetically.
   */
  Text = 'Text',
  /**
   * Sorts the data by numeric value, from low to high.
   */
  Number = 'Number',
  /**
   * Sorts the data by numeric value, from high to low.
   */
  NumberReversed = 'NumberReversed',
  /**
   * Sorts the data by boolean value.
   */
  Boolean = 'Boolean',
}

/**
 * Helper for sorting the data shown by an UI list. It remembers the sorting settings, even
 * after closing the app, and allows to open the modal window used on small screens for selecting
 * how to sort the data.
 */
export class DataSorter {
  // Columns that can be used for sorting the data.
  private sortableColumns: SortingColumn[];
  // Id of the list, for saving the data in localStorage.
  private id: string;
  // Currently selected column for sorting the data.
  private sortBy: SortingColumn;
  // If the data must be sorted in reversed order.
  private sortReverse = false;
  // Data to sort.
  private data: any[];
  // Index inside sortableColumns of the default column.
  private defaultColumnIndex: number;

  // Prefixes used, along the ID, for saving the sorting options in localStorage.
  private readonly columnStorageKeyPrefix = 'col_';
  private readonly orderStorageKeyPrefix = 'order_';

  private dataUpdatedSubject = new Subject<void>();

  /**
   * Returns the name of the icon that should be used in the column header for indicating
   * the sorting order.
   */
  get sortingArrow(): string {
    return this.sortReverse ? 'keyboard_arrow_up' : 'keyboard_arrow_down';
  }

  /**
   * Returns the column currently being used for sorting the data.
   */
  get currentSortingColumn(): SortingColumn {
    return this.sortBy;
  }

  /**
   * Returns if the data is being sorted in reverse order.
   */
  get sortingInReverseOrder(): boolean {
    return this.sortReverse;
  }

  /**
   * Emits every time the data is sorted.
   */
  get dataSorted(): Observable<void> {
    return this.dataUpdatedSubject.asObservable();
  }

  /**
   * @param columns Array with the data about the columns that can be used for sorting the list.
   * @param defaultColumnIndex Index in the "columns" array that must be used by default for
   * sorting the data. This column will be the one selected if not previously saved sorting
   * settings are found and will be used as tie-breaker when 2 columns have the same value.
   * @param id Unique short text identifying the list, for saving the sorting configuration
   * in localStorage.
   */
  constructor(
    private dialog: MatDialog,
    private translateService: TranslateService,
    columns: SortingColumn[],
    defaultColumnIndex: number,
    id: string,
  ) {
    this.sortableColumns = columns;
    this.id = id;
    this.defaultColumnIndex = defaultColumnIndex;
    this.sortBy = columns[defaultColumnIndex];

    // Restore any previously saved configuration.
    const savedColumn = localStorage.getItem(this.columnStorageKeyPrefix + id);
    if (savedColumn) {
      const savedColumnData = columns.find(column => column.id === savedColumn);
      if (savedColumnData) {
        this.sortBy = savedColumnData;
      }
    }

    this.sortReverse = localStorage.getItem(this.orderStorageKeyPrefix + id) === 'true';
  }

  /**
   * Must be called after finishing using the instance.
   */
  dispose() {
    this.dataUpdatedSubject.complete();
  }

  /**
   * Sets the data and inmediatelly sorts it. Each time this instance sorts the data,
   * the provided array is updated.
   * @param data Data to sort.
   */
  setData(data: any[]) {
    this.data = data;
    this.sortData();
  }

  /**
   * Changes the column and/or order used for sorting the data.
   */
  changeSortingOrder(column: SortingColumn) {
    if (this.sortBy !== column) {
      this.sortBy = column;
      this.sortReverse = false;

      localStorage.setItem(this.columnStorageKeyPrefix + this.id, column.id);
      localStorage.setItem(this.orderStorageKeyPrefix + this.id, String(this.sortReverse));
    } else {
      this.sortReverse = !this.sortReverse;

      localStorage.setItem(this.orderStorageKeyPrefix + this.id, String(this.sortReverse));
    }

    this.sortData();
  }

  /**
   * Opens the modal window used on small screens for selecting how to sort the data.
   */
  openSortingOrderModal() {
    // Create 2 options for every sortable column, for ascending and descending order.
    const options: SelectableOption[] = [];
    this.sortableColumns.forEach(column => {
      const label = this.translateService.instant(column.label);
      options.push({
        label: label + ' ' + this.translateService.instant('tables.ascending-order'),
      });
      options.push({
        label: label + ' ' + this.translateService.instant('tables.descending-order'),
      });
    });

    // Open the option selection modal window.
    SelectOptionComponent.openDialog(this.dialog, options, 'tables.title').afterClosed().subscribe((result: number) => {
      if (result) {
        result = (result - 1) / 2;
        const index = Math.floor(result);
        // Use the column and order selected by the user.
        this.sortBy = this.sortableColumns[index];
        this.sortReverse = result !== index;

        this.sortData();
      }
    });
  }

  /**
   * Sorts the data.
   */
  private sortData() {
    if (this.data) {
      // Sort all the data.
      this.data.sort((a, b) => {
        // Sort using the currently selected column.
        let response = this.getSortResponse(this.sortBy, a, b);
        // If the 2 values are equal, sort using the default column, if it is not already
        // the selected one.
        if (response === 0) {
          if (this.sortableColumns[this.defaultColumnIndex] !== this.sortBy) {
            response = this.getSortResponse(this.sortableColumns[this.defaultColumnIndex], a, b);
          }
        }

        return response;
      });

      // Inform the update.
      this.dataUpdatedSubject.next();
    }
  }

  /**
   * Returns the value needed by the "sort" function of the data array.
   * @param sortingColumn Column being used to sort the data.
   * @param a First value.
   * @param b Second value.
   */
  private getSortResponse(sortingColumn: SortingColumn, a, b) {
    // Get the data from the property.
    let aVal = a, bVal = b;
    sortingColumn.properties.forEach(property => {
      aVal = aVal[property];
      bVal = bVal[property];
    });

    // Use the selected sorting method.
    let response = 0;
    if (sortingColumn.sortingMode === SortingModes.Text) {
      response = !this.sortReverse ? (aVal as string).localeCompare(bVal) : (bVal as string).localeCompare(aVal);
    } else if (sortingColumn.sortingMode === SortingModes.NumberReversed) {
      response = !this.sortReverse ? bVal - aVal : aVal - bVal;
    } else if (sortingColumn.sortingMode === SortingModes.Number) {
      response = !this.sortReverse ? aVal - bVal : bVal - aVal;
    } else if (sortingColumn.sortingMode === SortingModes.Boolean) {
      if (aVal.is_up && !bVal.is_up) {
        response = -1;
      } else if (!aVal.is_up && bVal.is_up) {
        response = 1;
      }
      response = response * (this.sortReverse ? -1 : 1);
    }

    return response;
  }
}
