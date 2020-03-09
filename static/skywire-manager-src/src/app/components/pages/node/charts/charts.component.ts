import { Component, Input, OnChanges, SimpleChanges } from '@angular/core';

import { Transport } from '../../../../app.datatypes';

/**
 * Shows 2 line graps with the recent data upload/download activity of a node.
 */
@Component({
  selector: 'app-charts',
  templateUrl: './charts.component.html',
  styleUrls: ['./charts.component.scss']
})
export class ChartsComponent implements OnChanges {
  /**
   * The transports data of the node.
   */
  @Input() transports: Transport[];

  // Data for the graphs.
  sendingTotal = 0;
  receivingTotal = 0;
  sendingHistory = [0, 0, 0, 0, 0, 0, 0, 0, 0, 0];
  receivingHistory = [0, 0, 0, 0, 0, 0, 0, 0, 0, 0];

  private initialized = false;

  ngOnChanges(changes: SimpleChanges) {
    const transports: Transport[] = changes.transports.currentValue;

    if (transports) {
      this.sendingTotal = transports.reduce((total, transport) => total + transport.log.sent, 0);
      this.receivingTotal = transports.reduce((total, transport) => total + transport.log.recv, 0);

      // Populate the history arrays for the first time.
      if (!this.initialized) {
        for (let i = 0; i < 10; i++) {
          this.sendingHistory[i] = this.sendingTotal;
          this.receivingHistory[i] = this.receivingTotal;
        }

        this.initialized = true;
      }
    } else {
      this.sendingTotal = 0;
      this.receivingTotal = 0;
    }

    this.sendingHistory.push(this.sendingTotal);
    this.receivingHistory.push(this.receivingTotal);

    // Limit the history to 10 elements.
    if (this.sendingHistory.length > 10) {
      this.sendingHistory.splice(0, this.sendingHistory.length - 10);
      this.receivingHistory.splice(0, this.receivingHistory.length - 10);
    }
  }
}
