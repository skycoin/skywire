import { Component, Inject } from '@angular/core';
import { MatDialogRef, MAT_DIALOG_DATA } from '@angular/material/dialog';
import { TabButtonData } from '../tab-bar.component';

@Component({
  selector: 'app-select-tab',
  templateUrl: './select-tab.component.html',
  styleUrls: ['./select-tab.component.scss'],
})
export class SelectTabComponent {
  constructor(
    @Inject(MAT_DIALOG_DATA) public data: Transport,
    public dialogRef: MatDialogRef<TabButtonData[]>,
  ) { }

  closePopup(index: number) {
    this.dialogRef.close(index + 1);
  }
}
