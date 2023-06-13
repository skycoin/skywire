import { Component, OnInit, OnDestroy } from '@angular/core';
import { Subscription } from 'rxjs';

import { Node } from '../../../../../app.datatypes';
import { NodeComponent } from '../../node.component';
import { PageBaseComponent } from 'src/app/utils/page-base';

/**
 * Page for showing the complete list of the local shared ports of a node.
 */
@Component({
  selector: 'app-all-local-fwd-ports',
  templateUrl: './all-local-fwd-ports.component.html',
  styleUrls: ['./all-local-fwd-ports.component.scss']
})
export class AllLocalFwdPortsComponent extends PageBaseComponent implements OnInit, OnDestroy {
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
