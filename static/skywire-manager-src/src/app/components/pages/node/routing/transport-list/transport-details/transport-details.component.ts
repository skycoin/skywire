import { Component, Inject } from '@angular/core';
import { MAT_DIALOG_DATA, MatDialog, MatDialogRef, MatDialogConfig } from '@angular/material/dialog';
import { Transport } from '../../../../../../app.datatypes';
import { AppConfig } from 'src/app/app.config';

@Component({
  selector: 'app-transport-details',
  templateUrl: './transport-details.component.html',
  styleUrls: ['./transport-details.component.scss']
})
export class TransportDetailsComponent {

  public static openDialog(dialog: MatDialog, data: Transport): MatDialogRef<TransportDetailsComponent, any> {
    const config = new MatDialogConfig();
    config.data = data;
    config.autoFocus = false;
    config.width = AppConfig.largeModalWidth;

    return dialog.open(TransportDetailsComponent, config);
  }

  constructor(
    @Inject(MAT_DIALOG_DATA) public data: Transport,
  ) { }
}
