import { Component, Inject } from '@angular/core';
import { MAT_DIALOG_DATA, MatDialog, MatDialogRef, MatDialogConfig } from '@angular/material/dialog';

import { Transport } from '../../../../../../app.datatypes';
import { AppConfig } from 'src/app/app.config';

/**
 * Modal window for showing the details of a transport.
 */
@Component({
  selector: 'app-transport-details',
  templateUrl: './transport-details.component.html',
  styleUrls: ['./transport-details.component.scss']
})
export class TransportDetailsComponent {

  /**
   * Opens the modal window. Please use this function instead of opening the window "by hand".
   */
  public static openDialog(dialog: MatDialog, transport: Transport): MatDialogRef<TransportDetailsComponent, any> {
    const config = new MatDialogConfig();
    config.data = transport;
    config.autoFocus = false;
    config.width = AppConfig.largeModalWidth;

    return dialog.open(TransportDetailsComponent, config);
  }

  constructor(
    @Inject(MAT_DIALOG_DATA) public data: Transport,
    public dialogRef: MatDialogRef<TransportDetailsComponent>,
  ) { }
}
