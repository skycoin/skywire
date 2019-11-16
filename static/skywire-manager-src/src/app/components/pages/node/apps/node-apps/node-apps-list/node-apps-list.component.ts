import { Component, Input, OnDestroy } from '@angular/core';
import { Application } from '../../../../../../app.datatypes';
import { MatDialog, MatDialogRef } from '@angular/material/dialog';
import { MatTableDataSource } from '@angular/material/table';
import { AppsService } from '../../../../../../services/apps.service';
import { LogComponent } from '../log/log.component';
import { NodeComponent } from '../../../node.component';
import { Observable, Subscription } from 'rxjs';
import { ActivatedRoute } from '@angular/router';
import { AppConfig } from '../../../../../../app.config';
import GeneralUtils from '../../../../../../utils/generalUtils';
import { ConfirmationComponent } from '../../../../../layout/confirmation/confirmation.component';
import { SnackbarService } from '../../../../../../services/snackbar.service';

enum SortableColumns {
  Name,
  Port,
  Status,
  AutoStart,
}

@Component({
  selector: 'app-node-app-list',
  templateUrl: './node-apps-list.component.html',
  styleUrls: ['./node-apps-list.component.scss']
})
export class NodeAppsListComponent implements OnDestroy {
  @Input() nodePK: string;
  sortableColumns = SortableColumns;

  sortBy = SortableColumns.Name;
  sortReverse = false;
  get sortingArrow(): string {
    return this.sortReverse ? 'keyboard_arrow_up' : 'keyboard_arrow_down';
  }

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
        if ((startApps && this.appsMap.get(key).status !== 1) || (!startApps && this.appsMap.get(key).status === 1)) {
          elementsToChange.push(key);
        }
      }
    });

    if (startApps) {
      this.changeAppsValRecursively(elementsToChange, false, startApps);
    } else {
      const confirmationDialog = GeneralUtils.createDeleteConfirmation(this.dialog, 'apps.stop-selected-confirmation');

      confirmationDialog.componentInstance.operationAccepted.subscribe(() => {
        confirmationDialog.componentInstance.showProcessing();

        this.changeAppsValRecursively(elementsToChange, false, startApps, confirmationDialog);
      });
    }
  }

  changeAutostartOfSelected(autostart: boolean) {
    const elementsToChange: string[] = [];
    this.selections.forEach((val, key) => {
      if (val) {
        if ((autostart && !this.appsMap.get(key).autostart) || (!autostart && this.appsMap.get(key).autostart)) {
          elementsToChange.push(key);
        }
      }
    });

    const confirmationDialog = GeneralUtils.createDeleteConfirmation(
      this.dialog, autostart ? 'apps.disable-autostart-selected-confirmation' : 'apps.enable-autostart-selected-confirmation'
    );

    confirmationDialog.componentInstance.operationAccepted.subscribe(() => {
      confirmationDialog.componentInstance.showProcessing();

      this.changeAppsValRecursively(elementsToChange, true, autostart, confirmationDialog);
    });
  }

  onCloseAppClicked(appName: string): void {
    // this.appsService.closeApp(appName).subscribe();
  }

  changeAppState(app: Application): void {
    if (app.status !== 1) {
      this.changeSingleAppVal(
        this.startChangingAppState(app.name, app.status !== 1)
      );
    } else {
      const confirmationDialog = GeneralUtils.createDeleteConfirmation(this.dialog, 'apps.stop-confirmation');

      confirmationDialog.componentInstance.operationAccepted.subscribe(() => {
        confirmationDialog.componentInstance.showProcessing();

        this.changeSingleAppVal(
          this.startChangingAppState(app.name, app.status !== 1),
          confirmationDialog
        );
      });
    }
  }

  changeAppAutostart(app: Application): void {
    const confirmationDialog = GeneralUtils.createDeleteConfirmation(
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

  private changeSingleAppVal(
    observable: Observable<any>,
    confirmationDialog: MatDialogRef<ConfirmationComponent, any> = null) {

    observable.subscribe(
      () => {
        if (confirmationDialog) {
          confirmationDialog.close();
        }
        setTimeout(() => NodeComponent.refreshCurrentDisplayedData(), 50);
        this.snackbarService.showDone('apps.operation-completed');
      }, () => {
        setTimeout(() => NodeComponent.refreshCurrentDisplayedData(), 50);
        if (confirmationDialog) {
          confirmationDialog.componentInstance.showDone('confirmation.error-header-text', 'apps.error');
        } else {
          this.snackbarService.showError('apps.error');
        }
      }
    );
  }

  viewLogs(app: Application): void {
    LogComponent.openDialog(this.dialog, app);
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

  private recalculateElementsToShow() {
    this.currentPage = this.currentPageInUrl;

    if (this.allApps) {
      this.allApps.sort((a, b) => {
        const defaultOrder = a.name.localeCompare(b.name);

        let response: number;
        if (this.sortBy === SortableColumns.Name) {
          response = !this.sortReverse ? a.name.localeCompare(b.name) : b.name.localeCompare(a.name);
        } else if (this.sortBy === SortableColumns.Port) {
          response = !this.sortReverse ? a.port - b.port : b.port - a.port;
        } else if (this.sortBy === SortableColumns.Status) {
          response = !this.sortReverse ? b.status - a.status : a.status - b.status;
        } else if (this.sortBy === SortableColumns.AutoStart) {
          response = !this.sortReverse ? (b.autostart ? 1 : 0) - (a.autostart ? 1 : 0) : (a.autostart ? 1 : 0) - (b.autostart ? 1 : 0);
        } else {
          response = defaultOrder;
        }

        return response !== 0 ? response : defaultOrder;
      });

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

  private startChangingAppState(appName: string, startApp: boolean): Observable<any> {
    return this.appsService.changeAppState(NodeComponent.getCurrentNodeKey(), appName, startApp);
  }

  private startChangingAppAutostart(appName: string, autostart: boolean): Observable<any> {
    return this.appsService.changeAppAutostart(NodeComponent.getCurrentNodeKey(), appName, autostart);
  }

  private changeAppsValRecursively(
    names: string[],
    changingAutostart: boolean,
    newVal: boolean,
    confirmationDialog: MatDialogRef<ConfirmationComponent, any> = null) {

    if (!names || names.length === 0) {
      setTimeout(() => NodeComponent.refreshCurrentDisplayedData(), 50);
      this.snackbarService.showDone('apps.operation-completed');

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

    observable.subscribe(() => {
      names.pop();
      if (names.length === 0) {
        if (confirmationDialog) {
          confirmationDialog.close();
        }
        setTimeout(() => NodeComponent.refreshCurrentDisplayedData(), 50);
        this.snackbarService.showDone('apps.operation-completed');
      } else {
        this.changeAppsValRecursively(names, changingAutostart, newVal, confirmationDialog);
      }
    }, () => {
      setTimeout(() => NodeComponent.refreshCurrentDisplayedData(), 50);
      if (confirmationDialog) {
        confirmationDialog.componentInstance.showDone('confirmation.error-header-text', 'apps.error');
      } else {
        this.snackbarService.showError('apps.error');
      }
    });
  }
}
