import { Component, Inject } from '@angular/core';
import { MatDialogRef, MatDialog, MatDialogConfig, MAT_DIALOG_DATA } from '@angular/material/dialog';
import { AppConfig } from 'src/app/app.config';

export interface SelectableOption {
  icon: string;
  label: string;
}

@Component({
  selector: 'app-select-option',
  templateUrl: './select-option.component.html',
  styleUrls: ['./select-option.component.scss'],
})
export class SelectOptionComponent {
  public static openDialog(dialog: MatDialog, data: SelectableOption[]): MatDialogRef<SelectOptionComponent, any> {
    const config = new MatDialogConfig();
    config.data = data;
    config.autoFocus = false;
    config.width = AppConfig.smallModalWidth;

    return dialog.open(SelectOptionComponent, config);
  }

  constructor(
    @Inject(MAT_DIALOG_DATA) public data: SelectableOption[],
    public dialogRef: MatDialogRef<SelectOptionComponent>,
  ) { }

  closePopup(selectedOption: number) {
    this.dialogRef.close(selectedOption);
  }
}
