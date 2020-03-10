import { Injectable } from '@angular/core';
import { MatSnackBar } from '@angular/material/snack-bar';

import { SnackbarComponent, SnackbarIcons, SnackbarColors, SnackbarConfig } from '../components/layout/snack-bar/snack-bar.component';
import { OperationError } from '../utils/operation-error';
import { processServiceError } from '../utils/errors';

/**
 * Allows to easily show/hide the snackbar. For consistency, the snakbar should always be displayed
 * using this service.
 */
@Injectable({
  providedIn: 'root'
})
export class SnackbarService {
  /**
   * If the last snackbar shown was open to display a temporary error.
   */
  private lastWasTemporaryError = false;

  constructor(private snackBar: MatSnackBar) { }

  /**
   * Opens the snackbar and shows an error.
   * @param body Text or error to show.
   * @param textTranslationParams Params that must be passed to the "translate" pipe, if any.
   * @param isTemporalError True if the snackbar should be closed when calling "closeCurrentIfTemporaryError"
   * if it was not automatically closed before that.
   */
  public showError(body: string | OperationError, textTranslationParams: any = null, isTemporalError = false) {
    body = processServiceError(body);
    this.lastWasTemporaryError = isTemporalError;
    this.show(body.translatableErrorMsg, textTranslationParams, SnackbarIcons.Error, SnackbarColors.Red, 10000);
  }

  /**
   * Opens the snackbar and shows a warning.
   * @param textTranslationParams Params that must be passed to the "translate" pipe, if any.
   */
  public showWarning(text: string, textTranslationParams: any = null) {
    this.lastWasTemporaryError = false;
    this.show(text, textTranslationParams, SnackbarIcons.Warning, SnackbarColors.Yellow, 10000);
  }

  /**
   * Opens the snackbar and shows a success msg.
   * @param textTranslationParams Params that must be passed to the "translate" pipe, if any.
   */
  public showDone(text: string, textTranslationParams: any = null) {
    this.lastWasTemporaryError = false;
    this.show(text, textTranslationParams, SnackbarIcons.Done, SnackbarColors.Green, 5000);
  }

  /**
   * Closes the currently displayed snackbar.
   */
  public closeCurrent() {
    this.snackBar.dismiss();
  }

  /**
   * Closes the currently displayed snackbar, but only if it was opened to display a temporary error.
   * When opening a snackbar for displaying an error, it can be set as for a temporary error or not
   * at will. One example case when temporary errors showld be used is when having a connection error,
   * so if after retrying the connection the data is recovered and the snackbar is still open, it can
   * be closed by calling this function, so the user does not see the loading error and the loaded data
   * at the same time, and this function would also avoild the risk of closing another important error
   * snackbar that could have replaced the one with the loading error.
   */
  public closeCurrentIfTemporaryError() {
    if (this.lastWasTemporaryError) {
      this.snackBar.dismiss();
    }
  }

  private show(text: string, textTranslationParams: any, icon: SnackbarIcons, color: SnackbarColors, duration: number) {
    const config: SnackbarConfig = { text, textTranslationParams, icon, color };

    this.snackBar.openFromComponent(SnackbarComponent, {
      duration: duration,
      panelClass: 'p-0',
      data: config,
    });
  }
}
