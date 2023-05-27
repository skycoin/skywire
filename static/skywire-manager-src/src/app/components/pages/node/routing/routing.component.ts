import { Component, OnInit, OnDestroy } from '@angular/core';
import { Subscription } from 'rxjs';

import { Node, Route } from '../../../../app.datatypes';
import { NodeComponent } from '../node.component';
import { PageBaseComponent } from 'src/app/utils/page-base';
import { PingDialogComponent } from './ping-dialog/ping-dialog.component';
import { MatDialog } from '@angular/material/dialog';

/**
 * Page that shows the routing summary. It is a subpage of the Node page.
 */
@Component({
  selector: 'app-routing',
  templateUrl: './routing.component.html',
  styleUrls: ['./routing.component.scss']
})
export class RoutingComponent extends PageBaseComponent implements OnInit, OnDestroy {
  node: Node;
  routes: Route[];
  nodePK: string;

  private dataSubscription: Subscription;

  constructor(private dialog: MatDialog) {
    super();
  }

  ngOnInit() {
    // Get the node data from the parent page.
    this.dataSubscription = NodeComponent.currentNode.subscribe((node: Node) => {
      this.nodePK = node.localPk;
      this.node = node;
      this.routes = node.routes;
    });

    return super.ngOnInit();
  }

  ngOnDestroy() {
    this.dataSubscription.unsubscribe();
  }

  openPingDialog() {
    PingDialogComponent.openDialog(this.dialog);
  }
}
