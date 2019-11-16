import { Component, OnInit, OnDestroy } from '@angular/core';
import { Node } from '../../../../app.datatypes';
import { NodeComponent } from '../node.component';
import { Subscription } from 'rxjs';

@Component({
  selector: 'app-node-info',
  templateUrl: './node-info.component.html',
  styleUrls: ['./node-info.component.scss']
})
export class NodeInfoComponent implements OnInit, OnDestroy {
  node: Node;

  private dataSubscription: Subscription;

  ngOnInit() {
    this.dataSubscription = NodeComponent.currentNode.subscribe((node: Node) => {
      this.node = node;
    });
  }

  ngOnDestroy() {
    this.dataSubscription.unsubscribe();
  }
}
