import { Component, ElementRef, Inject, ViewChild } from '@angular/core';
import { MAT_DIALOG_DATA, MatDialogRef, MatDialog, MatDialogConfig } from '@angular/material/dialog';

import { AppConfig } from 'src/app/app.config';

export class RoutePreviewData {
  startPk: string;
  destinationPk: string;
  PkForNewTransport: string;
  connectionsFronStart: Set<string>;
  route: string;
}

@Component({
  selector: 'app-route-preview',
  templateUrl: './route-preview.component.html',
  styleUrls: ['./route-preview.component.scss']
})
export class RoutePreviewComponent {
  steps: string[] = [];
  completeRoute = false;

  public static openDialog(dialog: MatDialog, data: RoutePreviewData): MatDialogRef<RoutePreviewComponent, any> {
    const config = new MatDialogConfig();
    config.data = data;
    config.autoFocus = false;
    config.width = AppConfig.largeModalWidth;

    return dialog.open(RoutePreviewComponent, config);
  }

  constructor(
    @Inject(MAT_DIALOG_DATA) public data: RoutePreviewData,
    public dialogRef: MatDialogRef<RoutePreviewComponent>,
  ) {
    this.steps = data.route.split('/');

    if (!this.steps[this.steps.length - 1]) {
      this.steps.pop();
    }

    if (this.steps[this.steps.length - 1] !== data.startPk) {
      this.steps.push(data.startPk);
    }

    this.steps = this.steps.reverse();

    this.completeRoute = data.PkForNewTransport === data.startPk;
  }

  alreadyConnected(pk: string): boolean {
    if (pk === this.data.startPk) {
      return false;
    }

    return this.data.connectionsFronStart.has(pk);
  }
}
