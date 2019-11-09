import { Component, OnInit, OnDestroy } from '@angular/core';
import { Application, Node } from '../../../../app.datatypes';
import { NodeComponent } from '../node.component';
import { Subscription } from 'rxjs';

@Component({
  selector: 'app-apps',
  templateUrl: './apps.component.html',
  styleUrls: ['./apps.component.css']
})
export class AppsComponent implements OnInit, OnDestroy {
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
