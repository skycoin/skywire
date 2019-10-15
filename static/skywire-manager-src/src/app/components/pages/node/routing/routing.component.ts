import { Component, OnInit, OnDestroy } from '@angular/core';
import { Node, Transport, Route } from '../../../../app.datatypes';
import { NodeComponent } from '../node.component';
import { Subscription } from 'rxjs';

@Component({
  selector: 'app-routing',
  templateUrl: './routing.component.html',
  styleUrls: ['./routing.component.css']
})
export class RoutingComponent implements OnInit, OnDestroy {
  transports: Transport[];
  routes: Route[];

  private dataSubscription: Subscription;

  ngOnInit() {
    this.dataSubscription = NodeComponent.currentNode.subscribe((node: Node) => {
      this.transports = node.transports;
      this.routes = node.routes;
    });
  }

  ngOnDestroy() {
    this.dataSubscription.unsubscribe();
  }
}
