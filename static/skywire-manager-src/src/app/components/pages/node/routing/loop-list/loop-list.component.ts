import { Component, Input, OnChanges, OnInit } from '@angular/core';
import { MatTableDataSource } from '@angular/material';
import { Route } from '../../../../../app.datatypes';

@Component({
  selector: 'app-loop-list',
  templateUrl: './loop-list.component.html',
  styleUrls: ['./loop-list.component.css']
})
export class LoopListComponent implements OnInit, OnChanges {
  displayedColumns: string[] = ['key', 'rule'];
  dataSource = new MatTableDataSource<Route>();
  @Input() routes: Route[] = [];

  ngOnChanges(): void {
    this.dataSource.data = this.routes;
  }

  ngOnInit(): void {
    this.dataSource.data = this.routes;
  }
}
