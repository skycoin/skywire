import { Component, Inject } from '@angular/core';
import { MatDialogRef, MAT_DIALOG_DATA, MatDialog, MatDialogConfig } from '@angular/material/dialog';

import { AppConfig } from 'src/app/app.config';

/**
 * Data about a node.
 */
export interface NodeData {
  key: string;
  label: string;
  version: string;
  tag: string;
}

/**
 * Modal window used for updating all the nodes.
 */
@Component({
  selector: 'app-update-all',
  templateUrl: './update-all.component.html',
  styleUrls: ['./update-all.component.scss'],
})
export class UpdateAllComponent {
  updatableNodes: NodeData[];
  nonUpdatableNodes: NodeData[];

  /**
   * Opens the modal window. Please use this function instead of opening the window "by hand".
   * @param updatableNodes List with the nodes that can be updated with the terminal.
   * @param nonUpdatableNodes List with the nodes that can not be updated with the terminal.
   */
  public static openDialog(dialog: MatDialog, updatableNodes: NodeData[], nonUpdatableNodes: NodeData[]): MatDialogRef<UpdateAllComponent, any> {
    const config = new MatDialogConfig();
    config.data = [updatableNodes, nonUpdatableNodes];
    config.autoFocus = false;
    config.width = AppConfig.smallModalWidth;

    return dialog.open(UpdateAllComponent, config);
  }

  constructor(
    public dialogRef: MatDialogRef<UpdateAllComponent>,
    @Inject(MAT_DIALOG_DATA) data: NodeData[][],
  ) {
    this.updatableNodes = data[0];
    this.nonUpdatableNodes = data[1];
  }

  openTerminal(key: string) {
    const protocol = window.location.protocol;
    const hostname = window.location.host.replace('localhost:4200', '127.0.0.1:8000');
    window.open(protocol + '//' + hostname + '/pty/' + key + '?commands=update', '_blank', 'noopener noreferrer');
  }
}
