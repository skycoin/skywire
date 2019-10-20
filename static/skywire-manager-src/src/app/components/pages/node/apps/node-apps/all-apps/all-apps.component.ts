import { Component, OnInit, OnDestroy } from '@angular/core';
import { Node, Application } from '../../../../../../app.datatypes';
import { NodeComponent } from '../../../node.component';
import { Subscription } from 'rxjs';

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
    this.dataSubscription = NodeComponent.currentNode.subscribe((node: Node) => {
      this.nodePK = node.local_pk;
      this.apps = node.apps;
    });
  }

  ngOnDestroy() {
    this.dataSubscription.unsubscribe();
  }
}
