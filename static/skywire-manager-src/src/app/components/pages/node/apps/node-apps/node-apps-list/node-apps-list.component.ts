import { Component, Input, OnChanges, SimpleChanges } from '@angular/core';
import { Application } from '../../../../../../app.datatypes';
import {MatTableDataSource} from '@angular/material';
import {AppsService} from '../../../../../../services/apps.service';

@Component({
  selector: 'app-node-app-list',
  templateUrl: './node-apps-list.component.html',
  styleUrls: ['./node-apps-list.component.scss']
})
export class NodeAppsListComponent implements OnChanges {
  @Input() apps: Application[];
  displayedColumns: string[] = ['index', 'name', 'port', 'status', 'autostart', 'x'];
  dataSource = new MatTableDataSource<Application>();

  constructor(private appsService: AppsService) { }

  ngOnChanges(changes: SimpleChanges): void {
    this.dataSource.data = this.apps;
  }

  onCloseAppClicked(appName: string): void {
    // this.appsService.closeApp(appName).subscribe();
  }
}
