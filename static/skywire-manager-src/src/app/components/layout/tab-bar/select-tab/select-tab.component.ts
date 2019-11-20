import { Component, Inject } from '@angular/core';
import { MatDialogRef, MAT_DIALOG_DATA, MatDialog, MatDialogConfig } from '@angular/material/dialog';
import { TabButtonData } from '../tab-bar.component';
import { AppConfig } from 'src/app/app.config';

@Component({
  selector: 'app-select-tab',
  templateUrl: './select-tab.component.html',
  styleUrls: ['./select-tab.component.scss'],
})
export class SelectTabComponent {
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
