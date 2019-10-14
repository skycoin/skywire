import { Component, Input, OnChanges, SimpleChanges } from '@angular/core';
import { Application } from '../../../../../../app.datatypes';
import {MatTableDataSource, MatDialogConfig, MatDialog} from '@angular/material';
import {AppsService} from '../../../../../../services/apps.service';
import { LogComponent } from '../log/log.component';
import { NodeComponent } from '../../../node.component';
import { ErrorsnackbarService } from '../../../../../../services/errorsnackbar.service';
import { TranslateService } from '@ngx-translate/core';
import { Observable } from 'rxjs';

@Component({
  selector: 'app-node-app-list',
  templateUrl: './node-apps-list.component.html',
  styleUrls: ['./node-apps-list.component.scss']
})
export class NodeAppsListComponent implements OnChanges {
  @Input() apps: Application[];
  displayedColumns: string[] = ['selection', 'index', 'name', 'port', 'status', 'autostart', 'actions'];
  dataSource = new MatTableDataSource<Application>();
  selections = new Map<string, boolean>();
  appsMap: Map<string, Application>;

  constructor(
    private appsService: AppsService,
    private dialog: MatDialog,
    private errorSnackBar: ErrorsnackbarService,
    private translate: TranslateService,
  ) { }

  ngOnChanges(changes: SimpleChanges): void {
    this.dataSource.data = this.apps;

    if (this.apps) {
      this.appsMap = new Map<string, Application>();
      this.apps.forEach(app => {
        this.appsMap.set(app.name, app);

        if (!this.selections.has(app.name)) {
          this.selections.set(app.name, false);
        }
      });

      const keysToRemove: string[] = [];
      this.selections.forEach((val, key) => {
        if (!this.appsMap.has(key)) {
          keysToRemove.push(key);
        }
      });

      keysToRemove.forEach(key => {
        this.selections.delete(key);
      });
    }
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
        setTimeout(() => NodeComponent.refreshDisplayedData(), 50);
        this.errorSnackBar.open(this.translate.instant('apps.' + (app.status === 0 ? 'started' : 'stopped')));
      }, () => {
        setTimeout(() => NodeComponent.refreshDisplayedData(), 50);
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

  private startChangingAppState(appName: string, startApp: boolean, autostart: boolean): Observable<any> {
    return this.appsService.changeAppState(appName, startApp, autostart);
  }

  private changeAppStateRecursively(names: string[], startApp: boolean) {
    if (!names || names.length === 0) {
      setTimeout(() => NodeComponent.refreshDisplayedData(), 50);
      this.errorSnackBar.open(this.translate.instant('apps.' + (startApp ? 'started' : 'stopped')));

      return;
    }

    this.startChangingAppState(names[names.length - 1], startApp, this.appsMap.get(names[names.length - 1]).autostart).subscribe(() => {
      names.pop();
      if (names.length === 0) {
        setTimeout(() => NodeComponent.refreshDisplayedData(), 50);
        this.errorSnackBar.open(this.translate.instant('apps.' + (startApp ? 'started' : 'stopped')));
      } else {
        this.changeAppStateRecursively(names, startApp);
      }
    }, () => {
      setTimeout(() => NodeComponent.refreshDisplayedData(), 50);
      this.errorSnackBar.open(this.translate.instant('apps.error'));
    });
  }
}
