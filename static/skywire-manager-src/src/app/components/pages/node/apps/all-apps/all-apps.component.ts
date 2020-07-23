import { Component, OnInit, OnDestroy } from '@angular/core';
import { Subscription } from 'rxjs';

import { Node, Application } from '../../../../../app.datatypes';
import { NodeComponent } from '../../node.component';

/**
 * Page for showing the complete list of the apps of a node.
 */
@Component({
  selector: 'app-all-apps',
  templateUrl: './all-apps.component.html',
  styleUrls: ['./all-apps.component.scss']
})
export class AllAppsComponent implements OnInit, OnDestroy {
  apps: Application[];
  nodePK: string;

  private dataSubscription: Subscription;

  ngOnInit() {
    // Get the node data from the parent page.
    this.dataSubscription = NodeComponent.currentNode.subscribe((node: Node) => {
      this.nodePK = node.local_pk;
      this.apps = node.apps;
    });
  }

  ngOnDestroy() {
    this.dataSubscription.unsubscribe();
  }
}
