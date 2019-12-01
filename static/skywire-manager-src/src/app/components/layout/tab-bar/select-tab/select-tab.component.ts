import { Component, Inject } from '@angular/core';
import { MatDialogRef, MAT_DIALOG_DATA, MatDialog, MatDialogConfig } from '@angular/material/dialog';

import { TabButtonData } from '../tab-bar.component';
import { AppConfig } from 'src/app/app.config';

/**
 * Modal window for allowing the user to select a tab. When the user selects an option,
 * the modal window is closed and the number of the selected option (counting from 1) is
 * returned in the "afterClosed" envent.
 */
@Component({
  selector: 'app-select-tab',
  templateUrl: './select-tab.component.html',
  styleUrls: ['./select-tab.component.scss'],
})
export class SelectTabComponent {
  /**
   * Opens the modal window. Please use this function instead of opening the window "by hand".
   */
  public static openDialog(dialog: MatDialog, data: TabButtonData[]): MatDialogRef<SelectTabComponent, any> {
    const config = new MatDialogConfig();
    config.data = data;
    config.autoFocus = false;
    config.width = AppConfig.smallModalWidth;

    return dialog.open(SelectTabComponent, config);
  }

  constructor(
    @Inject(MAT_DIALOG_DATA) public data: TabButtonData[],
    public dialogRef: MatDialogRef<SelectTabComponent>,
  ) { }

  closePopup(index: number) {
    this.dialogRef.close(index + 1);
  }
}
