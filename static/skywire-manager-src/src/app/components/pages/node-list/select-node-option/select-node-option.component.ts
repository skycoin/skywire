import { Component } from '@angular/core';
import { MatDialogRef, MatDialog, MatDialogConfig } from '@angular/material/dialog';
import { AppConfig } from 'src/app/app.config';

export enum NodeListOptions {
  Rename = 1,
  Delete = 2,
}

interface Option {
  icon: string;
  label: string;
}

@Component({
  selector: 'app-select-node-option',
  templateUrl: './select-node-option.component.html',
  styleUrls: ['./select-node-option.component.scss'],
})
export class SelectNodeOptionComponent {
  nodeListOptions = NodeListOptions;
  options: Option[] = [
    {
      icon: 'short_text',
      label: 'edit-label.title',
    },
    {
      icon: 'close',
      label: 'nodes.delete-node',
    }
  ];

  public static openDialog(dialog: MatDialog): MatDialogRef<SelectNodeOptionComponent, any> {
    const config = new MatDialogConfig();
    config.autoFocus = false;
    config.width = AppConfig.smallModalWidth;

    return dialog.open(SelectNodeOptionComponent, config);
  }

  constructor(
    public dialogRef: MatDialogRef<SelectNodeOptionComponent>,
  ) { }

  closePopup(option: NodeListOptions) {
    this.dialogRef.close(option);
  }
}
