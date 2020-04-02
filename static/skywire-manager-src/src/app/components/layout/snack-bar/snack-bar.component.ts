import { Component, Inject } from '@angular/core';
import { MAT_SNACK_BAR_DATA, MatSnackBarRef } from '@angular/material/snack-bar';

/**
 * Icons SnackbarComponent can show.
 */
export enum SnackbarIcons {
  Error = 'error',
  Done = 'done',
  Warning = 'warning',
}

/**
 * Background colors SnackbarComponent can show.
 */
export enum SnackbarColors {
  Red = 'red-background',
  Green = 'green-background',
  Yellow = 'yellow-background',
}

/**
 * Configuration options for an instance of SnackbarComponent.
 */
export interface SnackbarConfig {
  /**
   * Text to show. Can be a variable for the "translate" pipe.
   */
  text: string;
  /**
   * Object to be passed to the "translate" pipe, to fill the params of the text.
   */
  textTranslationParams: any;
  /**
   * Text to show on the small lower line. Can be a variable for the "translate" pipe.
   */
  smallText: string;
  /**
   * Object to be passed to the "translate" pipe for smallText, to fill the params of the text.
   */
  smallTextTranslationParams: any;
  /**
   * Icon to show.
   */
  icon?: SnackbarIcons;
  /**
   * Background color.
   */
  color: SnackbarColors;
}

/**
 * Default snackbar for the app. It shows a text, a close button and an optional icon.
 * To show it, use SnackbarService.
 */
@Component({
  selector: 'app-snack-bar',
  templateUrl: './snack-bar.component.html',
  styleUrls: ['./snack-bar.component.scss'],
})
export class SnackbarComponent {
  config: SnackbarConfig;

  constructor(
    @Inject(MAT_SNACK_BAR_DATA) data: SnackbarConfig,
    private snackbarRef: MatSnackBarRef<SnackbarComponent>,
  ) {
    this.config = data;
  }

  close() {
    this.snackbarRef.dismiss();
  }
}
