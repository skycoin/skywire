import { Component, OnInit, Input, OnChanges } from '@angular/core';
import { MatTableDataSource } from '@angular/material';
import { Route } from 'src/app/app.datatypes';

@Component({
  selector: 'app-route-list',
  templateUrl: './route-list.component.html',
  styleUrls: ['./route-list.component.css']
})
export class RouteListComponent implements OnInit, OnChanges {
  displayedColumns: string[] = ['key', 'rule', 'x'];
  dataSource = new MatTableDataSource<Route>();
  @Input() routes: Route[] = [];

  ngOnChanges(): void {
    this.dataSource.data = this.routes;
  }

  ngOnInit(): void {
    this.dataSource.data = this.routes;
  }
}
