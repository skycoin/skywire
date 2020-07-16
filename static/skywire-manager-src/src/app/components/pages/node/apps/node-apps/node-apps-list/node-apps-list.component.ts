import { Component, Input, OnDestroy } from '@angular/core';
import { MatDialog, MatDialogRef } from '@angular/material/dialog';
import { Observable, Subscription } from 'rxjs';
import { ActivatedRoute, Router } from '@angular/router';

import { Application } from '../../../../../../app.datatypes';
import { AppsService } from '../../../../../../services/apps.service';
import { LogComponent } from '../log/log.component';
import { NodeComponent } from '../../../node.component';
import { AppConfig } from '../../../../../../app.config';
import GeneralUtils from '../../../../../../utils/generalUtils';
import { ConfirmationComponent } from '../../../../../layout/confirmation/confirmation.component';
import { SnackbarService } from '../../../../../../services/snackbar.service';
import { SelectableOption, SelectOptionComponent } from 'src/app/components/layout/select-option/select-option.component';
import { SkysocksSettingsComponent } from '../skysocks-settings/skysocks-settings.component';
import { processServiceError } from 'src/app/utils/errors';
import { OperationError } from 'src/app/utils/operation-error';
import { SkysocksClientSettingsComponent } from '../skysocks-client-settings/skysocks-client-settings.component';
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
  State = 'apps.apps-list.state',
  Name = 'apps.apps-list.app-name',
  Port = 'apps.apps-list.port',
  AutoStart = 'apps.apps-list.auto-start',
}

/**
 * Filters for the list. It is prepopulated with default data which indicates that no filter
 * has been selected. As the object may be included in the query string, prefixes are used to
 * avoid name collisions with other components in the same URL.
 */
class DataFilters {
  ap_state = '';
  ap_name = '';
  ap_port = '';
  ap_autostart = '';
}

/**
 * Shows the list of applications of a node. I can be used to show a short preview, with just some
 * elements and a link for showing the rest: or the full list, with pagination controls.
 */
@Component({
  selector: 'app-node-app-list',
  templateUrl: './node-apps-list.component.html',
  styleUrls: ['./node-apps-list.component.scss']
})
export class NodeAppsListComponent implements OnDestroy {
  private static sortByInternal = SortableColumns.Name;
  private static sortReverseInternal = false;

  @Input() nodePK: string;

  // Vars for keeping track of the column used for sorting the data.
  sortableColumns = SortableColumns;
  get sortBy(): SortableColumns { return NodeAppsListComponent.sortByInternal; }
  set sortBy(val: SortableColumns) { NodeAppsListComponent.sortByInternal = val; }
  get sortReverse(): boolean { return NodeAppsListComponent.sortReverseInternal; }
  set sortReverse(val: boolean) { NodeAppsListComponent.sortReverseInternal = val; }
  get sortingArrow(): string {
    return this.sortReverse ? 'keyboard_arrow_up' : 'keyboard_arrow_down';
  }

  dataSource: Application[];
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
    this.recalculateElementsToShow();
  }

  // List with the names of all the apps which can be configured directly on the manager.
  appsWithConfig = new Map<string, boolean>([
    ['skysocks', true],
    ['skysocks-client', true],
  ]);

  allApps: Application[];
  filteredApps: Application[];
  appsToShow: Application[];
  appsMap: Map<string, Application>;
  numberOfPages = 1;
  currentPage = 1;
  // Used as a helper var, as the URL is read asynchronously.
  currentPageInUrl = 1;
  @Input() set apps(val: Application[]) {
    this.allApps = val;
    this.filter();
  }

  // Array allowing to associate the properties of TransportFilters with the ones on the list
  // and the values that must be shown in the UI, for being able to use helper functions to
  // filter the data and show some UI elements.
  filterKeysAssociations: FilterKeysAssociation[] = [
    {
      filterName: 'apps.apps-list.filter-dialog.state',
      keyNameInElementsArray: 'status',
      keyNameInFiltersObject: 'ap_state',
      printableLabelsForValues: [
        {
          value: '',
          label: 'apps.apps-list.filter-dialog.state-options.any',
        },
        {
          value: '1',
          label: 'apps.apps-list.filter-dialog.state-options.running',
        },
        {
          value: '0',
          label: 'apps.apps-list.filter-dialog.state-options.stopped',
        }
      ],
    },
    {
      filterName: 'apps.apps-list.filter-dialog.name',
      keyNameInElementsArray: 'name',
      keyNameInFiltersObject: 'ap_name',
    },
    {
      filterName: 'apps.apps-list.filter-dialog.port',
      keyNameInElementsArray: 'port',
      keyNameInFiltersObject: 'ap_port',
    },
    {
      filterName: 'apps.apps-list.filter-dialog.autostart',
      keyNameInElementsArray: 'autostart',
      keyNameInFiltersObject: 'ap_autostart',
      printableLabelsForValues: [
        {
          value: '',
          label: 'apps.apps-list.filter-dialog.autostart-options.any',
        },
        {
          value: 'true',
          label: 'apps.apps-list.filter-dialog.autostart-options.enabled',
        },
        {
          value: 'false',
          label: 'apps.apps-list.filter-dialog.autostart-options.disabled',
        }
      ],
    },
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
    private appsService: AppsService,
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
  changeSelection(app: Application) {
    if (this.selections.get(app.name)) {
      this.selections.set(app.name, false);
    } else {
      this.selections.set(app.name, true);
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
   * Starts or stops the selected apps.
   */
  changeStateOfSelected(startApps: boolean) {
    const elementsToChange: string[] = [];
    // Ignore all elements shich already have the desired settings applied.
    this.selections.forEach((val, key) => {
      if (val) {
        if ((startApps && this.appsMap.get(key).status !== 1) || (!startApps && this.appsMap.get(key).status === 1)) {
          elementsToChange.push(key);
        }
      }
    });

    if (startApps) {
      this.changeAppsValRecursively(elementsToChange, false, startApps);
    } else {
      // Ask for confirmation if the apps are going to be stopped.
      const confirmationDialog = GeneralUtils.createConfirmationDialog(this.dialog, 'apps.stop-selected-confirmation');

      confirmationDialog.componentInstance.operationAccepted.subscribe(() => {
        confirmationDialog.componentInstance.showProcessing();

        this.changeAppsValRecursively(elementsToChange, false, startApps, confirmationDialog);
      });
    }
  }

  /**
   * Changes the autostart setting of the selected apps.
   */
  changeAutostartOfSelected(autostart: boolean) {
    const elementsToChange: string[] = [];
    // Ignore all elements shich already have the desired settings applied.
    this.selections.forEach((val, key) => {
      if (val) {
        if ((autostart && !this.appsMap.get(key).autostart) || (!autostart && this.appsMap.get(key).autostart)) {
          elementsToChange.push(key);
        }
      }
    });

    // Ask for confirmation.
    const confirmationDialog = GeneralUtils.createConfirmationDialog(
      this.dialog, autostart ? 'apps.enable-autostart-selected-confirmation' : 'apps.disable-autostart-selected-confirmation'
    );

    confirmationDialog.componentInstance.operationAccepted.subscribe(() => {
      confirmationDialog.componentInstance.showProcessing();

      this.changeAppsValRecursively(elementsToChange, true, autostart, confirmationDialog);
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
      type: FilterFieldTypes.Select,
      currentValue: this.currentFilters.ap_state,
      filterKeysAssociation: this.filterKeysAssociations[0]
    });
    filterFieldsParams.push({
      type: FilterFieldTypes.TextInput,
      currentValue: this.currentFilters.ap_name,
      filterKeysAssociation: this.filterKeysAssociations[1],
      maxlength: 50,
    });
    filterFieldsParams.push({
      type: FilterFieldTypes.TextInput,
      currentValue: this.currentFilters.ap_port,
      filterKeysAssociation: this.filterKeysAssociations[2],
      maxlength: 8,
    });
    filterFieldsParams.push({
      type: FilterFieldTypes.Select,
      currentValue: this.currentFilters.ap_autostart,
      filterKeysAssociation: this.filterKeysAssociations[3],
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
    if (this.allApps) {
      this.filteredApps = filterList(this.allApps, this.currentFilters, this.filterKeysAssociations);

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
  showOptionsDialog(app: Application) {
    const options: SelectableOption[] = [
      {
        icon: 'list',
        label: 'apps.view-logs',
      },
      {
        icon: app.status === 1 ? 'stop' : 'play_arrow',
        label: 'apps.' + (app.status === 1 ? 'stop-app' : 'start-app'),
      },
      {
        icon: app.autostart ? 'close' : 'done',
        label: app.autostart ? 'apps.apps-list.disable-autostart' : 'apps.apps-list.enable-autostart',
      }
    ];

    if (this.appsWithConfig.has(app.name)) {
      options.push({
        icon: 'settings',
        label: 'apps.settings',
      });
    }

    SelectOptionComponent.openDialog(this.dialog, options, 'common.options').afterClosed().subscribe((selectedOption: number) => {
      if (selectedOption === 1) {
        this.viewLogs(app);
      } else if (selectedOption === 2) {
        this.changeAppState(app);
      } else if (selectedOption === 3) {
        this.changeAppAutostart(app);
      } else if (selectedOption === 4) {
        this.config(app);
      }
    });
  }

  /**
   * Starts or stops a specific app.
   */
  changeAppState(app: Application): void {
    if (app.status !== 1) {
      this.changeSingleAppVal(
        this.startChangingAppState(app.name, app.status !== 1)
      );
    } else {
      // Ask for confirmation if the app is going to be stopped.
      const confirmationDialog = GeneralUtils.createConfirmationDialog(this.dialog, 'apps.stop-confirmation');

      confirmationDialog.componentInstance.operationAccepted.subscribe(() => {
        confirmationDialog.componentInstance.showProcessing();

        this.changeSingleAppVal(
          this.startChangingAppState(app.name, app.status !== 1),
          confirmationDialog
        );
      });
    }
  }

  /**
   * Changes the autostart setting of a specific app.
   */
  changeAppAutostart(app: Application): void {
    const confirmationDialog = GeneralUtils.createConfirmationDialog(
      this.dialog, app.autostart ? 'apps.disable-autostart-confirmation' : 'apps.enable-autostart-confirmation'
    );

    confirmationDialog.componentInstance.operationAccepted.subscribe(() => {
      confirmationDialog.componentInstance.showProcessing();

      this.changeSingleAppVal(
        this.startChangingAppAutostart(app.name, !app.autostart),
        confirmationDialog
      );
    });
  }

  /**
   * Helper function used for starting a process for changing a value on an app and reacting to the result.
   * Used to avoid repeating common code.
   * @param observable Observable which will start the operation after subscription.
   * @param confirmationDialog Dialog used for requesting confirmation from the user.
   */
  private changeSingleAppVal(
    observable: Observable<any>,
    confirmationDialog: MatDialogRef<ConfirmationComponent, any> = null) {

    // Start the operation and save it for posible cancellation.
    this.operationSubscriptionsGroup.push(observable.subscribe(
      () => {
        if (confirmationDialog) {
          confirmationDialog.close();
        }

        // Make the parent page reload the data.
        setTimeout(() => NodeComponent.refreshCurrentDisplayedData(), 50);
        this.snackbarService.showDone('apps.operation-completed');
      }, (err: OperationError) => {
        err = processServiceError(err);

        // Make the parent page reload the data.
        setTimeout(() => NodeComponent.refreshCurrentDisplayedData(), 50);

        if (confirmationDialog) {
          confirmationDialog.componentInstance.showDone('confirmation.error-header-text', err.translatableErrorMsg);
        } else {
          this.snackbarService.showError(err);
        }
      }
    ));
  }

  /**
   * Shows a modal window with the logs of an app.
   */
  viewLogs(app: Application): void {
    LogComponent.openDialog(this.dialog, app);
  }

  /**
   * Shows the appropriate modal window for configuring the app.
   */
  config(app: Application): void {
    if (app.name === 'skysocks') {
      SkysocksSettingsComponent.openDialog(this.dialog, app);
    } else if (app.name === 'skysocks-client') {
      SkysocksClientSettingsComponent.openDialog(this.dialog, app);
    } else {
      this.snackbarService.showError('apps.error');
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
    if (this.filteredApps) {
      // Sort all the data.
      this.filteredApps.sort((a, b) => {
        const defaultOrder = a.name.localeCompare(b.name);

        let response: number;
        if (this.sortBy === SortableColumns.Name) {
          response = !this.sortReverse ? a.name.localeCompare(b.name) : b.name.localeCompare(a.name);
        } else if (this.sortBy === SortableColumns.Port) {
          response = !this.sortReverse ? a.port - b.port : b.port - a.port;
        } else if (this.sortBy === SortableColumns.State) {
          response = !this.sortReverse ? b.status - a.status : a.status - b.status;
        } else if (this.sortBy === SortableColumns.AutoStart) {
          response = !this.sortReverse ? (b.autostart ? 1 : 0) - (a.autostart ? 1 : 0) : (a.autostart ? 1 : 0) - (b.autostart ? 1 : 0);
        } else {
          response = defaultOrder;
        }

        return response !== 0 ? response : defaultOrder;
      });

      // Calculate the pagination values.
      const maxElements = this.showShortList_ ? AppConfig.maxShortListElements : AppConfig.maxFullListElements;
      this.numberOfPages = Math.ceil(this.filteredApps.length / maxElements);
      if (this.currentPage > this.numberOfPages) {
        this.currentPage = this.numberOfPages;
      }

      // Limit the elements to show.
      const start = maxElements * (this.currentPage - 1);
      const end = start + maxElements;
      this.appsToShow = this.filteredApps.slice(start, end);

      // Create a map with the elements to show, as a helper.
      this.appsMap = new Map<string, Application>();
      this.appsToShow.forEach(app => {
        this.appsMap.set(app.name, app);

        // Add to the selections map the elements that are going to be shown.
        if (!this.selections.has(app.name)) {
          this.selections.set(app.name, false);
        }
      });

      // Remove from the selections map the elements that are not going to be shown.
      const keysToRemove: string[] = [];
      this.selections.forEach((value, key) => {
        if (!this.appsMap.has(key)) {
          keysToRemove.push(key);
        }
      });
      keysToRemove.forEach(key => {
        this.selections.delete(key);
      });
    } else {
      this.appsToShow = null;
      this.selections = new Map<string, boolean>();
    }

    this.dataSource = this.appsToShow;
  }

  /**
   * Prepares the operation for starting or stopping an app, but does not start it. To start the operation,
   * subscribe to the response.
   */
  private startChangingAppState(appName: string, startApp: boolean): Observable<any> {
    return this.appsService.changeAppState(NodeComponent.getCurrentNodeKey(), appName, startApp);
  }

  /**
   * Prepares the operation for changing the autostart setting of an app, but does not start it. To
   * start the operation, subscribe to the response.
   */
  private startChangingAppAutostart(appName: string, autostart: boolean): Observable<any> {
    return this.appsService.changeAppAutostart(NodeComponent.getCurrentNodeKey(), appName, autostart);
  }

  /**
   * Recursively changes a setting in a list of apps.
   * @param names List with the names of the apps to modify.
   * @param changingAutostart True if going to change the autostart setting, false if going to change
   * the running state of the apps.
   * @param newVal If "changingAutostart" is true, the new state of the autostart setting; otherwise,
   * true for starting the apps or false for stopping them.
   * @param confirmationDialog Dialog used for requesting confirmation from the user.
   */
  private changeAppsValRecursively(
    names: string[],
    changingAutostart: boolean,
    newVal: boolean,
    confirmationDialog: MatDialogRef<ConfirmationComponent, any> = null) {

    // The list may be empty because apps which already have the settings are ignored.
    if (!names || names.length === 0) {
      setTimeout(() => NodeComponent.refreshCurrentDisplayedData(), 50);
      this.snackbarService.showWarning('apps.operation-unnecessary');

      if (confirmationDialog) {
        confirmationDialog.close();
      }

      return;
    }

    let observable: Observable<any>;
    if (changingAutostart) {
      observable = this.startChangingAppAutostart(names[names.length - 1], newVal);
    } else {
      observable = this.startChangingAppState(names[names.length - 1], newVal);
    }

    this.operationSubscriptionsGroup.push(observable.subscribe(() => {
      names.pop();
      if (names.length === 0) {
        if (confirmationDialog) {
          confirmationDialog.close();
        }
        // Make the parent page reload the data.
        setTimeout(() => NodeComponent.refreshCurrentDisplayedData(), 50);
        this.snackbarService.showDone('apps.operation-completed');
      } else {
        this.changeAppsValRecursively(names, changingAutostart, newVal, confirmationDialog);
      }
    }, (err: OperationError) => {
      err = processServiceError(err);

      setTimeout(() => NodeComponent.refreshCurrentDisplayedData(), 50);
      if (confirmationDialog) {
        confirmationDialog.componentInstance.showDone('confirmation.error-header-text', err.translatableErrorMsg);
      } else {
        this.snackbarService.showError(err);
      }
    }));
  }
}
