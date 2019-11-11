import { Component, Inject } from '@angular/core';
import { MAT_DIALOG_DATA } from '@angular/material/dialog';
import { Transport } from '../../../../../../app.datatypes';

@Component({
  selector: 'app-transport-details',
  templateUrl: './transport-details.component.html',
  styleUrls: ['./transport-details.component.scss']
})
export class TransportDetailsComponent {

  constructor(
    @Inject(MAT_DIALOG_DATA) public data: Transport,
  ) { }
}
