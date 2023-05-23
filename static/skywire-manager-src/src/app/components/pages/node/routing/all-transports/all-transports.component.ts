import { Component, OnInit, OnDestroy } from '@angular/core';
import { Subscription } from 'rxjs';

import { Node } from '../../../../../app.datatypes';
import { NodeComponent } from '../../node.component';
import { PageBaseComponent } from 'src/app/utils/page-base';

/**
 * Page for showing the complete list of the transports of a node.
 */
@Component({
  selector: 'app-all-transports',
  templateUrl: './all-transports.component.html',
  styleUrls: ['./all-transports.component.scss']
})
export class AllTransportsComponent extends PageBaseComponent implements OnInit, OnDestroy {
  node: Node;

  private dataSubscription: Subscription;

  ngOnInit() {
    // Get the node data from the parent page.
    this.dataSubscription = NodeComponent.currentNode.subscribe((node: Node) => this.node = node);

    return super.ngOnInit();
  }

  ngOnDestroy() {
    this.dataSubscription.unsubscribe();
  }
}
