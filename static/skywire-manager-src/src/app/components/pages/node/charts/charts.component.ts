import { Component, OnInit, OnDestroy } from '@angular/core';
import { Subscription } from 'rxjs';

import { TrafficData, NodeService } from 'src/app/services/node.service';

/**
 * Shows 2 line graps with the recent data upload/download activity of a node.
 */
@Component({
  selector: 'app-charts',
  templateUrl: './charts.component.html',
  styleUrls: ['./charts.component.scss']
})
export class ChartsComponent implements OnInit, OnDestroy {
  data: TrafficData;

  private dataSubscription: Subscription;

  constructor(private nodeService: NodeService) { }

  ngOnInit() {
    this.dataSubscription = this.nodeService.specificNodeTrafficData.subscribe(data => {
      this.data = data;
    });
  }

  ngOnDestroy() {
    this.dataSubscription.unsubscribe();
  }
}
