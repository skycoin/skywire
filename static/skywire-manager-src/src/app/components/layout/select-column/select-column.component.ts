import { Component, Inject } from '@angular/core';
import { MatDialogRef, MAT_DIALOG_DATA, MatDialog, MatDialogConfig } from '@angular/material/dialog';
import { AppConfig } from 'src/app/app.config';

export interface SelectedColumn {
  label: string;
  sortReverse: boolean;
}

@Component({
  selector: 'app-select-column',
  templateUrl: './select-column.component.html',
  styleUrls: ['./select-column.component.scss'],
})
export class SelectColumnComponent {
  public static openDialog(dialog: MatDialog, data: string[]): MatDialogRef<SelectColumnComponent, any> {
    const config = new MatDialogConfig();
    config.data = data;
    config.autoFocus = false;
    config.width = AppConfig.smallModalWidth;

    return dialog.open(SelectColumnComponent, config);
  }

  constructor(
    @Inject(MAT_DIALOG_DATA) public data: string[],
    public dialogRef: MatDialogRef<SelectColumnComponent>,
  ) { }

  closePopup(label: string, reverse: boolean) {
    const response: SelectedColumn = {
      label: label,
      sortReverse: reverse,
    };

    this.dialogRef.close(response);
  }
}
