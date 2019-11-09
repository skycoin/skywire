import { Component, OnInit, OnDestroy } from '@angular/core';
import { Node, Route } from '../../../../../app.datatypes';
import { NodeComponent } from '../../node.component';
import { Subscription } from 'rxjs';

@Component({
  selector: 'app-all-routes',
  templateUrl: './all-routes.component.html',
  styleUrls: ['./all-routes.component.scss']
})
export class AllRoutesComponent implements OnInit, OnDestroy {
  routes: Route[];
  nodePK: string;

  private dataSubscription: Subscription;

  ngOnInit() {
    this.dataSubscription = NodeComponent.currentNode.subscribe((node: Node) => {
      this.nodePK = node.local_pk;
      this.routes = node.routes;
    });
  }

  ngOnDestroy() {
    this.dataSubscription.unsubscribe();
  }
}
