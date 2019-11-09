import { Component, OnInit, OnDestroy } from '@angular/core';
import { Node, Transport } from '../../../../../app.datatypes';
import { NodeComponent } from '../../node.component';
import { Subscription } from 'rxjs';

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
    this.dataSubscription = NodeComponent.currentNode.subscribe((node: Node) => {
      this.nodePK = node.local_pk;
      this.transports = node.transports;
    });
  }

  ngOnDestroy() {
    this.dataSubscription.unsubscribe();
  }
}
