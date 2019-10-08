import { Component, OnInit } from '@angular/core';
import { NodeService } from '../../../../services/node.service';
import { Node, Transport } from '../../../../app.datatypes';

@Component({
  selector: 'app-routing',
  templateUrl: './routing.component.html',
  styleUrls: ['./routing.component.css']
})
export class RoutingComponent implements OnInit {
  transports: Transport[];
  routes = [
    { key: 1, rule: '0sad76ds876a56fs86g9d7h9dfg676sa' },
    { key: 2, rule: '7g6f89s7sfs0sf7g97d6h5g4h434h3jj' },
  ];

  constructor(
    private nodeService: NodeService,
  ) { }

  ngOnInit() {
    this.nodeService.node().subscribe((node: Node) => {
      this.transports = node.transports;
    });
  }

}
