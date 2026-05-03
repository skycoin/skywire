import { Component, OnDestroy, OnInit } from '@angular/core';
import { Subscription } from 'rxjs';

import { Node } from '../../../../app.datatypes';
import { NodeComponent } from '../node.component';
import { PageBaseComponent } from 'src/app/utils/page-base';

/**
 * Visor "Resources" tab — host + process resource monitor on its
 * own page. Wraps app-resource-monitor with [expanded]=true so the
 * panel polls immediately when the user navigates here.
 */
@Component({
  selector: 'app-node-resources',
  templateUrl: './node-resources.component.html',
  standalone: false,
})
export class NodeResourcesComponent extends PageBaseComponent implements OnInit, OnDestroy {
  node: Node;
  private dataSubscription: Subscription;

  ngOnInit() {
    this.dataSubscription = NodeComponent.currentNode.subscribe((node: Node) => {
      this.node = node;
    });
    return super.ngOnInit();
  }

  ngOnDestroy() {
    if (this.dataSubscription) { this.dataSubscription.unsubscribe(); }
  }
}
