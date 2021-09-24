import { Component, OnInit, OnDestroy } from '@angular/core';
import { Subscription } from 'rxjs';

import { Node, Route } from '../../../../app.datatypes';
import { NodeComponent } from '../node.component';

/**
 * Page that shows the routing summary. It is a subpage of the Node page.
 */
@Component({
  selector: 'app-routing',
  templateUrl: './routing.component.html',
  styleUrls: ['./routing.component.scss']
})
export class RoutingComponent implements OnInit, OnDestroy {
  node: Node;
  routes: Route[];
  nodePK: string;

  private dataSubscription: Subscription;

  ngOnInit() {
    // Get the node data from the parent page.
    this.dataSubscription = NodeComponent.currentNode.subscribe((node: Node) => {
      this.nodePK = node.localPk;
      this.node = node;
      this.routes = node.routes;
    });
  }

  ngOnDestroy() {
    this.dataSubscription.unsubscribe();
  }
}
