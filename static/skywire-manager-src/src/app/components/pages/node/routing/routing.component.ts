import { Component, OnInit } from '@angular/core';
import { NodeService } from '../../../../services/node.service';
import { Node, Transport, Route } from '../../../../app.datatypes';

@Component({
  selector: 'app-routing',
  templateUrl: './routing.component.html',
  styleUrls: ['./routing.component.css']
})
export class RoutingComponent implements OnInit {
  transports: Transport[];
  routes: Route[];

  constructor(
    private nodeService: NodeService,
  ) { }

  ngOnInit() {
    this.nodeService.node().subscribe((node: Node) => {
      this.transports = node.transports;
      this.routes = node.routes;
    });
  }
}
