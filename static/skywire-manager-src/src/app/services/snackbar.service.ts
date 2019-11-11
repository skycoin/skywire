import { Injectable } from '@angular/core';
import { MatSnackBar } from '@angular/material/snack-bar';
import { SnackbarComponent, SnackbarIcons, SnackbarColors, SnackbarConfig } from '../components/layout/snack-bar/snack-bar.component';

@Injectable({
  providedIn: 'root'
})
export class SnackbarService {
  private lastWasTemporalError = false;

  constructor(private snackBar: MatSnackBar) { }

  public showError(text: string, textTranslationParams: any = null, isTemporalError = false) {
    this.lastWasTemporalError = isTemporalError;
    this.show(text, textTranslationParams, SnackbarIcons.Error, SnackbarColors.Red, 10000);
  }

  public showWarning(text: string, textTranslationParams: any = null) {
    this.lastWasTemporalError = false;
    this.show(text, textTranslationParams, SnackbarIcons.Warning, SnackbarColors.Yellow, 10000);
  }

  public showDone(text: string, textTranslationParams: any = null) {
    this.lastWasTemporalError = false;
    this.show(text, textTranslationParams, SnackbarIcons.Done, SnackbarColors.Green, 5000);
  }

  public closeCurrent() {
    this.snackBar.dismiss();
  }

  public closeCurrentIfTemporalError() {
    if (this.lastWasTemporalError) {
      this.snackBar.dismiss();
    }
  }

  private show(text: string, textTranslationParams: any, icon: SnackbarIcons, color: SnackbarColors, duration: number) {
    const config: SnackbarConfig = { text, textTranslationParams, icon, color };

    this.snackBar.openFromComponent(SnackbarComponent, {
      duration: duration,
      panelClass: 'custom-snack-bar',
      data: config,
    });
  }
}
