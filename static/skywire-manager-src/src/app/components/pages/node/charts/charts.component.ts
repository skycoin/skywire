import { Component, Input, OnChanges, SimpleChanges } from '@angular/core';
import { Transport } from '../../../../app.datatypes';

@Component({
  selector: 'app-charts',
  templateUrl: './charts.component.html',
  styleUrls: ['./charts.component.scss']
})
export class ChartsComponent implements OnChanges {
  @Input() transports: Transport[];
  sendingTotal = 0;
  receivingTotal = 0;
  sendingHistory = [0, 0, 0, 0, 0, 0, 0, 0, 0, 0];
  receivingHistory = [0, 0, 0, 0, 0, 0, 0, 0, 0, 0];

  ngOnChanges(changes: SimpleChanges) {
    const transports: Transport[] = changes.transports.currentValue;

    if (transports) {
      this.sendingTotal = transports.reduce((total, transport) => total + transport.log.sent, 0);
      this.receivingTotal = transports.reduce((total, transport) => total + transport.log.recv, 0);
    } else {
      this.sendingTotal = 0;
      this.receivingTotal = 0;
    }

    this.sendingHistory.push(this.sendingTotal);
    this.receivingHistory.push(this.receivingTotal);

    if (this.sendingHistory.length > 10) {
      this.sendingHistory.splice(0, this.sendingHistory.length - 10);
      this.receivingHistory.splice(0, this.receivingHistory.length - 10);
    }
  }
}
