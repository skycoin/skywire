import { Component, OnInit, OnDestroy } from '@angular/core';
import { Subscription } from 'rxjs';

import { Node, Route } from '../../../../app.datatypes';
import { NodeComponent } from '../node.component';
import { PageBaseComponent } from 'src/app/utils/page-base';
import { TrafficData } from 'src/app/services/single-node-data.service';

/**
 * Page that shows the routing summary. It is a subpage of the Node page.
 */
@Component({
    selector: 'app-routing',
    templateUrl: './routing.component.html',
    styleUrls: ['./routing.component.scss'],
    standalone: false
})
export class RoutingComponent extends PageBaseComponent implements OnInit, OnDestroy {
  node: Node;
  routes: Route[];
  nodePK: string;
  trafficData: TrafficData;

  private dataSubscription: Subscription;
  private trafficSubscription: Subscription;

  ngOnInit() {
    // Get the node data from the parent page.
    this.dataSubscription = NodeComponent.currentNode.subscribe((node: Node) => {
      this.nodePK = node.localPk;
      this.node = node;
      this.routes = node.routes;
    });
    // The Traffic chart used to live on the right-bar (visor info
    // pane); it's now anchored to this routing page since that's
    // where the data it summarizes (transports + routes) lives.
    this.trafficSubscription = NodeComponent.currentTrafficData.subscribe((td: TrafficData) => {
      this.trafficData = td;
    });

    return super.ngOnInit();
  }

  ngOnDestroy() {
    this.dataSubscription.unsubscribe();
    if (this.trafficSubscription) {
      this.trafficSubscription.unsubscribe();
    }
  }
}
