import { Component, OnInit, OnDestroy } from '@angular/core';
import { Subscription } from 'rxjs';

import { Node, Transport } from '../../../../../app.datatypes';
import { NodeComponent } from '../../node.component';

/**
 * Page for showing the complete list of the transports of a node.
 */
@Component({
  selector: 'app-all-transports',
  templateUrl: './all-transports.component.html',
  styleUrls: ['./all-transports.component.scss']
})
export class AllTransportsComponent implements OnInit, OnDestroy {
  transports: Transport[];
  nodePK: string;

  private dataSubscription: Subscription;

  ngOnInit() {
    // Get the node data from the parent page.
    this.dataSubscription = NodeComponent.currentNode.subscribe((node: Node) => {
      this.nodePK = node.localPk;
      this.transports = node.transports;
    });
  }

  ngOnDestroy() {
    this.dataSubscription.unsubscribe();
  }
}
