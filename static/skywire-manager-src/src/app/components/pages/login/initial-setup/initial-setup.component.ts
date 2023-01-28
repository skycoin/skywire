import { Component } from '@angular/core';
import { MatDialog, MatDialogRef, MatDialogConfig } from '@angular/material/dialog';

import { AppConfig } from 'src/app/app.config';

/**
 * Modal window for setting the node password for the first time.
 */
@Component({
  selector: 'app-initial-setup',
  templateUrl: './initial-setup.component.html',
  styleUrls: ['./initial-setup.component.scss']
})
export class InitialSetupComponent {

  /**
   * Opens the modal window. Please use this function instead of opening the window "by hand".
   */
  public static openDialog(dialog: MatDialog): MatDialogRef<InitialSetupComponent, any> {
    const config = new MatDialogConfig();
    config.autoFocus = false;
    config.width = AppConfig.smallModalWidth;

    return dialog.open(InitialSetupComponent, config);
  }

  constructor(
    public dialogRef: MatDialogRef<InitialSetupComponent>,
  ) { }
}
