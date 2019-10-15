import { Component, OnInit, Input, OnChanges } from '@angular/core';
import { MatTableDataSource, MatDialog, MatDialogConfig } from '@angular/material';
import { Route } from 'src/app/app.datatypes';
import { RouteService } from '../../../../../services/route.service';
import { ErrorsnackbarService } from '../../../../../services/errorsnackbar.service';
import { TranslateService } from '@ngx-translate/core';
import { NodeComponent } from '../../node.component';
import { RouteDetailsComponent } from './route-details/route-details.component';
import { Observable } from 'rxjs';

@Component({
  selector: 'app-route-list',
  templateUrl: './route-list.component.html',
  styleUrls: ['./route-list.component.css']
})
export class RouteListComponent implements OnInit, OnChanges {
  displayedColumns: string[] = ['selection', 'key', 'rule', 'details', 'x'];
  dataSource = new MatTableDataSource<Route>();
  @Input() routes: Route[] = [];
  selections = new Map<number, boolean>();

  constructor(
    private routeService: RouteService,
    private errorSnackBar: ErrorsnackbarService,
    private translate: TranslateService,
    private dialog: MatDialog,
  ) { }

  ngOnChanges(): void {
    this.dataSource.data = this.routes;

    if (this.routes) {
      const obtainedElementsMap = new Map<number, boolean>();
      this.routes.forEach(route => {
        obtainedElementsMap.set(route.key, true);

        if (!this.selections.has(route.key)) {
          this.selections.set(route.key, false);
        }
      });

      const keysToRemove: number[] = [];
      this.selections.forEach((val, key) => {
        if (!obtainedElementsMap.has(key)) {
          keysToRemove.push(key);
        }
      });

      keysToRemove.forEach(key => {
        this.selections.delete(key);
      });
    }
  }

  ngOnInit(): void {
    this.dataSource.data = this.routes;
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
