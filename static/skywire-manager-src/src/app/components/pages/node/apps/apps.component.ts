import { Component, OnInit } from '@angular/core';
import { Application } from '../../../../app.datatypes';
import { NodeService } from '../../../../services/node.service';

@Component({
  selector: 'app-apps',
  templateUrl: './apps.component.html',
  styleUrls: ['./apps.component.css']
})
export class AppsComponent implements OnInit {
  apps: Application[];

  constructor(
    private nodeService: NodeService,
  ) { }

  ngOnInit() {
    this.nodeService.node().subscribe(node => this.apps = node.apps);
  }
}
