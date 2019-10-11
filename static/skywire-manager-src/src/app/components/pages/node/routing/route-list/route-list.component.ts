import { Component, OnInit, Input, OnChanges } from '@angular/core';
import { MatTableDataSource, MatDialog, MatDialogConfig } from '@angular/material';
import { Route } from 'src/app/app.datatypes';
import { NodeService } from '../../../../../services/node.service';
import { RouteService } from '../../../../../services/route.service';
import { ErrorsnackbarService } from '../../../../../services/errorsnackbar.service';
import { TranslateService } from '@ngx-translate/core';
import { NodeComponent } from '../../node.component';
import { RouteDetailsComponent } from './route-details/route-details.component';

@Component({
  selector: 'app-route-list',
  templateUrl: './route-list.component.html',
  styleUrls: ['./route-list.component.css']
})
export class RouteListComponent implements OnInit, OnChanges {
  displayedColumns: string[] = ['key', 'rule', 'details', 'x'];
  dataSource = new MatTableDataSource<Route>();
  @Input() routes: Route[] = [];

  constructor(
    private nodeService: NodeService,
    private routeService: RouteService,
    private errorSnackBar: ErrorsnackbarService,
    private translate: TranslateService,
    private dialog: MatDialog,
  ) { }

  ngOnChanges(): void {
    this.dataSource.data = this.routes;
  }

  ngOnInit(): void {
    this.dataSource.data = this.routes;
  }

  details(route: string) {
    const config = new MatDialogConfig();
    config.data = route;
    config.autoFocus = false;
    this.dialog.open(RouteDetailsComponent, config);
  }

  delete(route: string) {
    this.routeService.delete(this.nodeService.getCurrentNodeKey(), route).subscribe(() => {
      NodeComponent.refreshDisplayedData();
      this.errorSnackBar.open(this.translate.instant('routes.deleted'));
    }, () => {
      this.errorSnackBar.open(this.translate.instant('routes.error-deleting'));
    });
  }
}
