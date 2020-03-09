import { Component, Inject } from '@angular/core';
import { MatDialogRef, MAT_DIALOG_DATA, MatDialog, MatDialogConfig } from '@angular/material/dialog';

import { AppConfig } from 'src/app/app.config';

/**
 * Result returned when a column is selected with SelectColumnComponent.
 */
export interface SelectedColumn {
  /**
   * Label of the selected column. Is the value of the element in the data array used for opening
   * the modal window.
   */
  label: string;
  /**
   * If the user selected the ascending (false) or descending (true) order.
   */
  sortReverse: boolean;
}

/**
 * Modal window shown on small screens for allowing the user to select which column use to sort
 * a table/list. It shows an ascending and a descending option for each column. When the user
 * selects a column, the modal window is closed and a "SelectedColumn" object is returned in
 * the "afterClosed" envent.
 */
@Component({
  selector: 'app-select-column',
  templateUrl: './select-column.component.html',
  styleUrls: ['./select-column.component.scss'],
})
export class SelectColumnComponent {
  /**
   * Opens the modal window. Please use this function instead of opening the window "by hand".
   */
  public static openDialog(dialog: MatDialog, columnNames: string[]): MatDialogRef<SelectColumnComponent, any> {
    const config = new MatDialogConfig();
    config.data = columnNames;
    config.autoFocus = false;
    config.width = AppConfig.smallModalWidth;

    return dialog.open(SelectColumnComponent, config);
  }

  constructor(
    @Inject(MAT_DIALOG_DATA) public data: string[],
    private dialogRef: MatDialogRef<SelectColumnComponent>,
  ) { }

  closePopup(label: string, reverse: boolean) {
    const response: SelectedColumn = {
      label: label,
      sortReverse: reverse,
    };

    this.dialogRef.close(response);
  }
}
