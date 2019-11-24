import { Component, Input, OnDestroy } from '@angular/core';
import { MatDialog, MatDialogRef } from '@angular/material/dialog';
import { Route } from 'src/app/app.datatypes';
import { RouteService } from '../../../../../services/route.service';
import { NodeComponent } from '../../node.component';
import { RouteDetailsComponent } from './route-details/route-details.component';
import { Observable, Subscription } from 'rxjs';
import { ActivatedRoute } from '@angular/router';
import { AppConfig } from '../../../../../app.config';
import GeneralUtils from '../../../../../utils/generalUtils';
import { ConfirmationComponent } from '../../../../layout/confirmation/confirmation.component';
import { SnackbarService } from '../../../../../services/snackbar.service';
import { SelectOptionComponent, SelectableOption } from 'src/app/components/layout/select-option/select-option.component';
import { SelectColumnComponent, SelectedColumn } from 'src/app/components/layout/select-column/select-column.component';

enum SortableColumns {
  Key = 'routes.key',
  Rule = 'routes.rule',
}

@Component({
  selector: 'app-route-list',
  templateUrl: './route-list.component.html',
  styleUrls: ['./route-list.component.scss']
})
export class RouteListComponent implements OnDestroy {
  @Input() nodePK: string;
  sortableColumns = SortableColumns;

  sortBy = SortableColumns.Key;
  sortReverse = false;
  get sortingArrow(): string {
    return this.sortReverse ? 'keyboard_arrow_up' : 'keyboard_arrow_down';
  }

  dataSource: Route[];
  selections = new Map<number, boolean>();

  showShortList_: boolean;
  @Input() set showShortList(val: boolean) {
    this.showShortList_ = val;
    this.recalculateElementsToShow();
  }

  allRoutes: Route[];
  routesToShow: Route[];
  numberOfPages = 1;
  currentPage = 1;
  currentPageInUrl = 1;
  @Input() set routes(val: Route[]) {
    this.allRoutes = val;
    this.recalculateElementsToShow();
  }

  private navigationsSubscription: Subscription;

  constructor(
    private routeService: RouteService,
    private dialog: MatDialog,
    private route: ActivatedRoute,
    private snackbarService: SnackbarService,
  ) {
    this.navigationsSubscription = this.route.paramMap.subscribe(params => {
      if (params.has('page')) {
        let selectedPage = Number.parseInt(params.get('page'), 10);
        if (selectedPage === NaN || selectedPage < 0) {
          selectedPage = 0;
        }

        this.currentPageInUrl = selectedPage;

        this.recalculateElementsToShow();
      }
    });
  }

  ngOnDestroy() {
    this.navigationsSubscription.unsubscribe();
  }

  changeSelection(route: Route) {
    if (this.selections.get(route.key)) {
      this.selections.set(route.key, false);
    } else {
      this.selections.set(route.key, true);
    }
  }

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

  changeAllSelections(setSelected: boolean) {
    this.selections.forEach((val, key) => {
      this.selections.set(key, setSelected);
    });
  }

  deleteSelected() {
    const confirmationDialog = GeneralUtils.createDeleteConfirmation(this.dialog, 'routes.delete-selected-confirmation');

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

    SelectOptionComponent.openDialog(this.dialog, options).afterClosed().subscribe((selectedOption: number) => {
      if (selectedOption === 1) {
        this.details(route.key.toString());
      } else if (selectedOption === 2) {
        this.delete(route.key);
      }
    });
  }

  details(route: string) {
    RouteDetailsComponent.openDialog(this.dialog, route);
  }

  delete(routeKey: number) {
    const confirmationDialog = GeneralUtils.createDeleteConfirmation(this.dialog, 'routes.delete-confirmation');

    confirmationDialog.componentInstance.operationAccepted.subscribe(() => {
      confirmationDialog.componentInstance.showProcessing();

      this.startDeleting(routeKey).subscribe(() => {
        confirmationDialog.close();
        NodeComponent.refreshCurrentDisplayedData();
        this.snackbarService.showDone('routes.deleted');
      }, () => {
        confirmationDialog.componentInstance.showDone('confirmation.error-header-text', 'routes.error-deleting');
      });
    });
  }

  changeSortingOrder(column: SortableColumns) {
    if (this.sortBy !== column) {
      this.sortBy = column;
      this.sortReverse = false;
    } else {
      this.sortReverse = !this.sortReverse;
    }

    this.recalculateElementsToShow();
  }

  openSortingOrderModal() {
    const enumKeys = Object.keys(SortableColumns);
    const columnsMap = new Map<string, SortableColumns>();
    const columns = enumKeys.map(key => {
      const val = SortableColumns[key as any];
      columnsMap.set(val, SortableColumns[key]);

      return val;
    });

    SelectColumnComponent.openDialog(this.dialog, columns).afterClosed().subscribe((result: SelectedColumn) => {
      if (result) {
        if (columnsMap.has(result.label) && (result.sortReverse !== this.sortReverse || columnsMap.get(result.label) !== this.sortBy)) {
          this.sortBy = columnsMap.get(result.label);
          this.sortReverse = result.sortReverse;

          this.recalculateElementsToShow();
        }
      }
    });
  }

  private recalculateElementsToShow() {
    this.currentPage = this.currentPageInUrl;

    if (this.allRoutes) {
      this.allRoutes.sort((a, b) => {
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

      const maxElements = this.showShortList_ ? AppConfig.maxShortListElements : AppConfig.maxFullListElements;

      this.numberOfPages = Math.ceil(this.allRoutes.length / maxElements);
      if (this.currentPage > this.numberOfPages) {
        this.currentPage = this.numberOfPages;
      }

      const start = maxElements * (this.currentPage - 1);
      const end = start + maxElements;
      this.routesToShow = this.allRoutes.slice(start, end);

      const currentElementsMap = new Map<number, boolean>();
      this.routesToShow.forEach(route => {
        currentElementsMap.set(route.key, true);

        if (!this.selections.has(route.key)) {
          this.selections.set(route.key, false);
        }
      });

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

  private startDeleting(routeKey: number): Observable<any> {
    return this.routeService.delete(NodeComponent.getCurrentNodeKey(), routeKey.toString());
  }

  deleteRecursively(ids: number[], confirmationDialog: MatDialogRef<ConfirmationComponent, any>) {
    this.startDeleting(ids[ids.length - 1]).subscribe(() => {
      ids.pop();
      if (ids.length === 0) {
        confirmationDialog.close();
        NodeComponent.refreshCurrentDisplayedData();
        this.snackbarService.showDone('routes.deleted');
      } else {
        this.deleteRecursively(ids, confirmationDialog);
      }
    }, () => {
      NodeComponent.refreshCurrentDisplayedData();
      confirmationDialog.componentInstance.showDone('confirmation.error-header-text', 'routes.error-deleting');
    });
  }
}
