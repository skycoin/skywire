import { Component, OnInit, OnDestroy } from '@angular/core';
import { Subscription } from 'rxjs';

import { Node, Route } from '../../../../../app.datatypes';
import { NodeComponent } from '../../node.component';
import { PageBaseComponent } from 'src/app/utils/page-base';

/**
 * Page for showing the complete list of the routes of a node.
 */
@Component({
  selector: 'app-all-routes',
  templateUrl: './all-routes.component.html',
  styleUrls: ['./all-routes.component.scss']
})
export class AllRoutesComponent extends PageBaseComponent implements OnInit, OnDestroy {
  routes: Route[];
  nodePK: string;

  private dataSubscription: Subscription;

  ngOnInit() {
    // Get the node data from the parent page.
    this.dataSubscription = NodeComponent.currentNode.subscribe((node: Node) => {
      this.nodePK = node.localPk;
      this.routes = node.routes;
    });

    return super.ngOnInit();
  }

  ngOnDestroy() {
    this.dataSubscription.unsubscribe();
  }
}
