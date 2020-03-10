import { Component, Inject } from '@angular/core';
import { MatDialogRef, MAT_DIALOG_DATA, MatDialog, MatDialogConfig } from '@angular/material/dialog';

import { AppConfig } from 'src/app/app.config';

/**
 * Modal window for selecting what page to show in a multipage list. When the user selects a page,
 * the modal window is closed and the number of the selected page is returned in the "afterClosed" envent.
 */
@Component({
  selector: 'app-select-page',
  templateUrl: './select-page.component.html',
  styleUrls: ['./select-page.component.scss'],
})
export class SelectPageComponent {
  options: number[] = [];

  /**
   * Opens the modal window. Please use this function instead of opening the window "by hand".
   */
  public static openDialog(dialog: MatDialog, pagesToSelectFrom: number): MatDialogRef<SelectPageComponent, any> {
    const config = new MatDialogConfig();
    config.data = pagesToSelectFrom;
    config.autoFocus = false;
    config.width = AppConfig.smallModalWidth;

    return dialog.open(SelectPageComponent, config);
  }

  constructor(
    @Inject(MAT_DIALOG_DATA) public data: any,
    private dialogRef: MatDialogRef<SelectPageComponent>,
  ) {
    for (let i = 0; i < data; i++) {
      this.options.push(i + 1);
    }
  }

  closePopup(page: string) {
    this.dialogRef.close(page);
  }
}
