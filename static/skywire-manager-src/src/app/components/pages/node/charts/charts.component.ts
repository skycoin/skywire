import { Component, Input } from '@angular/core';

import { TrafficData } from 'src/app/services/single-node-data.service';

/**
 * Shows 2 line graphs with the recent data upload/download activity of a node.
 */
@Component({
  selector: 'app-charts',
  templateUrl: './charts.component.html',
  styleUrls: ['./charts.component.scss']
})
export class ChartsComponent {
  @Input() trafficData: TrafficData;
}
