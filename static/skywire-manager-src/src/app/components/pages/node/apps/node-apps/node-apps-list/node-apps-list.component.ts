import { Component, Input, OnDestroy } from '@angular/core';
import { Application } from '../../../../../../app.datatypes';
import {MatTableDataSource, MatDialogConfig, MatDialog} from '@angular/material';
import {AppsService} from '../../../../../../services/apps.service';
import { LogComponent } from '../log/log.component';
import { NodeComponent } from '../../../node.component';
import { ErrorsnackbarService } from '../../../../../../services/errorsnackbar.service';
import { TranslateService } from '@ngx-translate/core';
import { Observable, Subscription } from 'rxjs';
import { ActivatedRoute } from '@angular/router';
import { AppConfig } from '../../../../../../app.config';

@Component({
  selector: 'app-node-app-list',
  templateUrl: './node-apps-list.component.html',
  styleUrls: ['./node-apps-list.component.scss']
})
export class NodeAppsListComponent implements OnDestroy {
  @Input() nodePK: string;

  displayedColumns: string[] = ['selection', 'name', 'port', 'status', 'autostart', 'actions'];
  dataSource = new MatTableDataSource<Application>();
  selections = new Map<string, boolean>();

  showShortList_: boolean;
  @Input() set showShortList(val: boolean) {
    this.showShortList_ = val;
    this.recalculateElementsToShow();
  }

  allApps: Application[];
  appsToShow: Application[];
  appsMap: Map<string, Application>;
  numberOfPages = 1;
  currentPage = 1;
  currentPageInUrl = 1;
  @Input() set apps(val: Application[]) {
    this.allApps = val;
    this.recalculateElementsToShow();
  }

  private navigationsSubscription: Subscription;

  constructor(
    private appsService: AppsService,
    private dialog: MatDialog,
    private errorSnackBar: ErrorsnackbarService,
    private translate: TranslateService,
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

  changeSelection(app: Application) {
    if (this.selections.get(app.name)) {
      this.selections.set(app.name, false);
    } else {
      this.selections.set(app.name, true);
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

  changeStateOfSelected(startApps: boolean) {
    const elementsToChange: string[] = [];
    this.selections.forEach((val, key) => {
      if (val) {
        if ((startApps && this.appsMap.get(key).status === 0) || (!startApps && this.appsMap.get(key).status === 1)) {
          elementsToChange.push(key);
        }
      }
    });

    this.changeAppStateRecursively(elementsToChange, startApps);
  }

  onCloseAppClicked(appName: string): void {
    // this.appsService.closeApp(appName).subscribe();
  }

  changeAppState(app: Application): void {
    this.startChangingAppState(app.name, app.status === 0, app.autostart).subscribe(
      () => {
        setTimeout(() => NodeComponent.refreshCurrentDisplayedData(), 50);
        this.errorSnackBar.open(this.translate.instant('apps.' + (app.status === 0 ? 'started' : 'stopped')));
      }, () => {
        setTimeout(() => NodeComponent.refreshCurrentDisplayedData(), 50);
        this.errorSnackBar.open(this.translate.instant('apps.error'));
      }
    );
  }

  viewLogs(app: Application): void {
    const config = new MatDialogConfig();
    config.data = app;
    config.autoFocus = false;
    this.dialog.open(LogComponent, config);
  }

  private recalculateElementsToShow() {
    this.currentPage = this.currentPageInUrl;

    if (this.allApps) {
      const maxElements = this.showShortList_ ? AppConfig.maxShortListElements : AppConfig.maxFullListElements;

      this.numberOfPages = Math.ceil(this.allApps.length / maxElements);
      if (this.currentPage > this.numberOfPages) {
        this.currentPage = this.numberOfPages;
      }

      const start = maxElements * (this.currentPage - 1);
      const end = start + maxElements;
      this.appsToShow = this.allApps.slice(start, end);

      this.appsMap = new Map<string, Application>();
      this.appsToShow.forEach(app => {
        this.appsMap.set(app.name, app);

        if (!this.selections.has(app.name)) {
          this.selections.set(app.name, false);
        }
      });

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

    this.dataSource.data = this.appsToShow;
  }

  private startChangingAppState(appName: string, startApp: boolean, autostart: boolean): Observable<any> {
    return this.appsService.changeAppState(NodeComponent.getCurrentNodeKey(), appName, startApp, autostart);
  }

  private changeAppStateRecursively(names: string[], startApp: boolean) {
    if (!names || names.length === 0) {
      setTimeout(() => NodeComponent.refreshCurrentDisplayedData(), 50);
      this.errorSnackBar.open(this.translate.instant('apps.' + (startApp ? 'started' : 'stopped')));

      return;
    }

    this.startChangingAppState(names[names.length - 1], startApp, this.appsMap.get(names[names.length - 1]).autostart).subscribe(() => {
      names.pop();
      if (names.length === 0) {
        setTimeout(() => NodeComponent.refreshCurrentDisplayedData(), 50);
        this.errorSnackBar.open(this.translate.instant('apps.' + (startApp ? 'started' : 'stopped')));
      } else {
        this.changeAppStateRecursively(names, startApp);
      }
    }, () => {
      setTimeout(() => NodeComponent.refreshCurrentDisplayedData(), 50);
      this.errorSnackBar.open(this.translate.instant('apps.error'));
    });
  }
}
