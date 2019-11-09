import { Component, Inject } from '@angular/core';
import { MAT_SNACK_BAR_DATA, MatSnackBarRef } from '@angular/material';

export enum SnackbarIcons {
  Error = 'error',
  Done = 'done',
  Warning = 'warning',
}

export enum SnackbarColors {
  Red = 'red-background',
  Green = 'green-background',
  Yellow = 'yellow-background',
}

export interface SnackbarConfig {
  text: string;
  textTranslationParams: any;
  icon: SnackbarIcons;
  color: SnackbarColors;
}

@Component({
  selector: 'app-snack-bar',
  templateUrl: './snack-bar.component.html',
  styleUrls: ['./snack-bar.component.scss'],
})
export class SnackbarComponent {
  config: SnackbarConfig;

  constructor(
    @Inject(MAT_SNACK_BAR_DATA) public data: SnackbarConfig,
    public snackbarRef: MatSnackBarRef<SnackbarComponent>,
  ) {
    this.config = data;
  }

  close() {
    this.snackbarRef.dismiss();
  }
}
