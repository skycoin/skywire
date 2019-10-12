import { Component, Input, OnChanges, SimpleChanges } from '@angular/core';
import { Application } from '../../../../../../app.datatypes';
import {MatTableDataSource, MatDialogConfig, MatDialog} from '@angular/material';
import {AppsService} from '../../../../../../services/apps.service';
import { LogComponent } from '../log/log.component';
import { NodeComponent } from '../../../node.component';
import { ErrorsnackbarService } from '../../../../../../services/errorsnackbar.service';
import { TranslateService } from '@ngx-translate/core';

@Component({
  selector: 'app-node-app-list',
  templateUrl: './node-apps-list.component.html',
  styleUrls: ['./node-apps-list.component.scss']
})
export class NodeAppsListComponent implements OnChanges {
  @Input() apps: Application[];
  displayedColumns: string[] = ['index', 'name', 'port', 'status', 'autostart', 'actions'];
  dataSource = new MatTableDataSource<Application>();

  constructor(
    private appsService: AppsService,
    private dialog: MatDialog,
    private errorSnackBar: ErrorsnackbarService,
    private translate: TranslateService,
  ) { }

  ngOnChanges(changes: SimpleChanges): void {
    this.dataSource.data = this.apps;
  }

  onCloseAppClicked(appName: string): void {
    // this.appsService.closeApp(appName).subscribe();
  }

  changeAppState(app: Application): void {
    this.appsService.changeAppState(app.name, app.status === 0, app.autostart).subscribe(
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
}
