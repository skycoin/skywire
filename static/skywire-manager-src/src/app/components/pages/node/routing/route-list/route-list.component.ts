import { Component, Input, OnDestroy } from '@angular/core';
import { MatTableDataSource, MatDialog, MatDialogConfig } from '@angular/material';
import { Route } from 'src/app/app.datatypes';
import { RouteService } from '../../../../../services/route.service';
import { ErrorsnackbarService } from '../../../../../services/errorsnackbar.service';
import { TranslateService } from '@ngx-translate/core';
import { NodeComponent } from '../../node.component';
import { RouteDetailsComponent } from './route-details/route-details.component';
import { Observable, Subscription } from 'rxjs';
import { ActivatedRoute } from '@angular/router';
import { AppConfig } from '../../../../../app.config';

@Component({
  selector: 'app-route-list',
  templateUrl: './route-list.component.html',
  styleUrls: ['./route-list.component.scss']
})
export class RouteListComponent implements OnDestroy {
  @Input() nodePK: string;

  displayedColumns: string[] = ['selection', 'key', 'rule', 'details', 'x'];
  dataSource = new MatTableDataSource<Route>();
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
    private errorSnackBar: ErrorsnackbarService,
    private translate: TranslateService,
    private dialog: MatDialog,
    private route: ActivatedRoute,
  ) {
    this.navigationsSubscription = this.route.paramMap.subscribe(params => {
      if (params.has('page')) {
        let selectedPage = Number.parseInt(params.get('page'));
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
    const elementsToRemove: number[] = [];
    this.selections.forEach((val, key) => {
      if (val) {
        elementsToRemove.push(key);
      }
    });

    this.deleteRecursively(elementsToRemove);
  }

  details(route: string) {
    const config = new MatDialogConfig();
    config.data = route;
    config.autoFocus = false;
    this.dialog.open(RouteDetailsComponent, config);
  }

  delete(routeKey: number) {
    this.startDeleting(routeKey).subscribe(() => {
      NodeComponent.refreshCurrentDisplayedData();
      this.errorSnackBar.open(this.translate.instant('routes.deleted'));
    }, () => {
      this.errorSnackBar.open(this.translate.instant('routes.error-deleting'));
    });
  }

  private recalculateElementsToShow() {
    this.currentPage = this.currentPageInUrl;

    if (this.allRoutes) {
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

    this.dataSource.data = this.routesToShow;
  }

  private startDeleting(routeKey: number): Observable<any> {
    return this.routeService.delete(NodeComponent.getCurrentNodeKey(), routeKey.toString());
  }

  deleteRecursively(ids: number[]) {
    this.startDeleting(ids[ids.length - 1]).subscribe(() => {
      ids.pop();
      if (ids.length === 0) {
        NodeComponent.refreshCurrentDisplayedData();
        this.errorSnackBar.open(this.translate.instant('routes.deleted'));
      } else {
        this.deleteRecursively(ids);
      }
    }, () => {
      NodeComponent.refreshCurrentDisplayedData();
      this.errorSnackBar.open(this.translate.instant('routes.error-deleting'));
    });
  }
}
