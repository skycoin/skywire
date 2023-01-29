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
import { FilterProperties, FilterFieldTypes } from 'src/app/utils/filters';
import { SortingColumn, SortingModes, DataSorter } from 'src/app/utils/lists/data-sorter';
import { DataFilterer } from 'src/app/utils/lists/data-filterer';
import { LabeledElementTextComponent } from 'src/app/components/layout/labeled-element-text/labeled-element-text.component';
import { LabeledElementTypes, StorageService } from 'src/app/services/storage.service';

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
  // Small text for identifying the list, needed for the helper objects.
  private readonly listId = 'rl';

  @Input() nodePK: string;

  // Vars with the data of the columns used for sorting the data.
  keySortData = new SortingColumn(['key'], 'routes.key', SortingModes.Number);
  typeSortData = new SortingColumn(['type'], 'routes.type', SortingModes.Number);
  sourceSortData = new SortingColumn(['src'], 'routes.source', SortingModes.Text, ['src_label']);
  destinationSortData = new SortingColumn(['dst'], 'routes.destination', SortingModes.Text, ['dst_label']);

  labeledElementTypes = LabeledElementTypes;

  private dataSortedSubscription: Subscription;
  private dataFiltererSubscription: Subscription;
  // Objects in charge of sorting and filtering the data.
  dataSorter: DataSorter;
  dataFilterer: DataFilterer;

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
    // Sort the data.
    this.dataSorter.setData(this.filteredRoutes);
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

    // Include additional properties with helpful data. Mostly for displating the data and for
    // using the filterer and sorter objects.
    this.allRoutes.forEach(route => {
      // Save the type in the root of the object to be able to use it with the filterer.
      if (route.ruleSummary.ruleType || route.ruleSummary.ruleType === 0) {
        route['type'] = route.ruleSummary.ruleType;
      } else {
        route['type'] = '';
      }

      if (route.appFields || route.forwardFields) {
        const routeDescriptor = route.appFields ? route.appFields.routeDescriptor : route.forwardFields.routeDescriptor;

        // Save the source and destination visor keys and the associated labels.
        route['src'] = routeDescriptor.srcPk;
        route['src_label'] =
          LabeledElementTextComponent.getCompleteLabel(this.storageService, this.translateService, route['src']);
        route['dst'] = routeDescriptor.dstPk;
        route['dst_label'] =
          LabeledElementTextComponent.getCompleteLabel(this.storageService, this.translateService, route['dst']);
      } else if (route.intermediaryForwardFields) {
        // Save the destination transport id and the associated label. There is no source transport.
        route['src'] = '';
        route['src_label'] = '';
        route['dst'] = route.intermediaryForwardFields.nextTid;
        route['dst_label'] =
          LabeledElementTextComponent.getCompleteLabel(this.storageService, this.translateService, route['dst']);
      } else {
        // Special case.
        route['src'] = '';
        route['src_label'] = '';
        route['dst'] = '';
        route['dst_label'] = '';
      }
    });

    this.dataFilterer.setData(this.allRoutes);
  }

  // Array with the properties of the columns that can be used for filtering the data.
  filterProperties: FilterProperties[] = [
    {
      filterName: 'routes.filter-dialog.key',
      keyNameInElementsArray: 'key',
      type: FilterFieldTypes.TextInput,
      maxlength: 8,
    },
    {
      filterName: 'routes.filter-dialog.source',
      keyNameInElementsArray: 'src',
      secondaryKeyNameInElementsArray: 'src_label',
      type: FilterFieldTypes.TextInput,
      maxlength: 66,
    },
    {
      filterName: 'routes.filter-dialog.destination',
      keyNameInElementsArray: 'dst',
      secondaryKeyNameInElementsArray: 'dst_label',
      type: FilterFieldTypes.TextInput,
      maxlength: 66,
    },
  ];

  /**
   * Map with the types of route rules that the hypervisor can return and are known by this app.
   */
  private ruleTypes = new Map<number, string>([
    [0, 'App'],
    [1, 'Forward'],
    [2, 'Int. forward']
  ]);

  private navigationsSubscription: Subscription;
  private operationSubscriptionsGroup: Subscription[] = [];

  constructor(
    private routeService: RouteService,
    private dialog: MatDialog,
    private route: ActivatedRoute,
    private router: Router,
    private snackbarService: SnackbarService,
    private translateService: TranslateService,
    private storageService: StorageService,
  ) {
    // Initialize the data sorter.
    const sortableColumns: SortingColumn[] = [
      this.keySortData,
      this.typeSortData,
      this.sourceSortData,
      this.destinationSortData,
    ];
    this.dataSorter = new DataSorter(this.dialog, this.translateService, this.storageService, sortableColumns, 0, this.listId);
    this.dataSortedSubscription = this.dataSorter.dataSorted.subscribe(() => {
      // When this happens, the data in allRoutes has already been sorted.
      this.recalculateElementsToShow();
    });

    // Include the known route types in the filterer config array.
    const typeFilterConfig: FilterProperties = {
      filterName: 'routes.filter-dialog.type',
      keyNameInElementsArray: 'type',
      type: FilterFieldTypes.Select,
      printableLabelsForValues: [
        {
          value: '',
          label: 'routes.filter-dialog.any-type-option',
        }
      ],
    };
    this.ruleTypes.forEach((v, k) => {
      typeFilterConfig.printableLabelsForValues.push({
        value: k + '',
        label: v,
      });
    });
    this.filterProperties = [typeFilterConfig].concat(this.filterProperties);

    // Initialize the data filterer.
    this.dataFilterer = new DataFilterer(this.dialog, this.route, this.router, this.filterProperties, this.listId);
    this.dataFiltererSubscription = this.dataFilterer.dataFiltered.subscribe(data => {
      this.filteredRoutes = data;
      this.dataSorter.setData(this.filteredRoutes);
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
  }

  ngOnDestroy() {
    this.navigationsSubscription.unsubscribe();
    this.operationSubscriptionsGroup.forEach(sub => sub.unsubscribe());
    this.dataSortedSubscription.unsubscribe();
    this.dataFiltererSubscription.unsubscribe();
    this.dataSorter.dispose();
    this.dataFilterer.dispose();
  }

  /**
   * Asks the node data to be refreshed.
   */
  refreshData() {
    NodeComponent.refreshCurrentDisplayedData();
  }

  /**
   * Gets the name of a route type.
   */
  getTypeName(type: number): string {
    if (this.ruleTypes.has(type)) {
      return this.ruleTypes.get(type);
    }

    return 'Unknown';
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
        this.details(route);
      } else if (selectedOption === 2) {
        this.delete(route.key);
      }
    });
  }

  /**
   * Shows a modal window with the details of a route.
   */
  details(route: Route) {
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
   * Recalculates which elements should be shown on the UI.
   */
  private recalculateElementsToShow() {
    // Needed to prevent racing conditions.
    this.currentPage = this.currentPageInUrl;

    // Needed to prevent racing conditions.
    if (this.filteredRoutes) {
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
