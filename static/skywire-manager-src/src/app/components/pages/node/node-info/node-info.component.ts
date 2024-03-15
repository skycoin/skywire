import { Component, OnInit, OnDestroy } from '@angular/core';
import { Subscription } from 'rxjs';
import { TrafficData } from 'src/app/services/single-node-data.service';

import { Node } from '../../../../app.datatypes';
import { NodeComponent } from '../node.component';
import { PageBaseComponent } from 'src/app/utils/page-base';

/**
 * Page for showing the basic info of a node.
 */
@Component({
  selector: 'app-node-info',
  templateUrl: './node-info.component.html',
  styleUrls: ['./node-info.component.scss']
})
export class NodeInfoComponent extends PageBaseComponent implements OnInit, OnDestroy {
  node: Node;
  trafficData: TrafficData;

  private nodeSubscription: Subscription;
  private trafficDataSubscription: Subscription;

  ngOnInit() {
    // Get the node and data transmission data from the parent page.
    this.nodeSubscription = NodeComponent.currentNode.subscribe((node: Node) => {
      this.node = node;
    });
    this.trafficDataSubscription = NodeComponent.currentTrafficData.subscribe((data: TrafficData) => {
      this.trafficData = data;
    });

    return super.ngOnInit();
  }

  ngOnDestroy() {
    this.nodeSubscription.unsubscribe();
    this.trafficDataSubscription.unsubscribe();
  }
}
