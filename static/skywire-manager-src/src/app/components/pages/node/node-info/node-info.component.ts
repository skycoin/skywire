import { Component, OnInit, OnDestroy } from '@angular/core';
import { Subscription } from 'rxjs';

import { Node } from '../../../../app.datatypes';
import { NodeComponent } from '../node.component';
import { PageBaseComponent } from 'src/app/utils/page-base';

/**
 * Page for showing the basic info of a node.
 */
@Component({
    selector: 'app-node-info',
    templateUrl: './node-info.component.html',
    styleUrls: ['./node-info.component.scss'],
    standalone: false
})
export class NodeInfoComponent extends PageBaseComponent implements OnInit, OnDestroy {
  node: Node;

  private nodeSubscription: Subscription;

  ngOnInit() {
    this.nodeSubscription = NodeComponent.currentNode.subscribe((node: Node) => {
      this.node = node;
    });

    return super.ngOnInit();
  }

  ngOnDestroy() {
    this.nodeSubscription.unsubscribe();
  }
}
