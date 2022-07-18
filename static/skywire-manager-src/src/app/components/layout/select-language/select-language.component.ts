import { Component, OnInit, OnDestroy } from '@angular/core';
import { MatDialogRef, MatDialog, MatDialogConfig } from '@angular/material/dialog';
import { Subscription } from 'rxjs';

import { LanguageData, LanguageService } from '../../../services/language.service';
import { AppConfig } from 'src/app/app.config';

/**
 * Modal window for changing UI language. It changes the language by itself.
 */
@Component({
  selector: 'app-select-language',
  templateUrl: './select-language.component.html',
  styleUrls: ['./select-language.component.scss'],
})
export class SelectLanguageComponent implements OnInit, OnDestroy {
  languages: LanguageData[] = [];

  private subscription: Subscription;

  /**
   * Opens the modal window. Please use this function instead of opening the window "by hand".
   */
  public static openDialog(dialog: MatDialog): MatDialogRef<SelectLanguageComponent, any> {
    const config = new MatDialogConfig();
    config.autoFocus = false;
    config.width = AppConfig.mediumModalWidth;

    return dialog.open(SelectLanguageComponent, config);
  }

  constructor(
    public dialogRef: MatDialogRef<SelectLanguageComponent>,
    private languageService: LanguageService,
  ) { }

  ngOnInit() {
    this.subscription = this.languageService.languages.subscribe(languages => {
      this.languages = languages;
    });
  }

  ngOnDestroy() {
    this.subscription.unsubscribe();
  }

  closePopup(language: LanguageData = null) {
    if (language) {
      this.languageService.changeLanguage(language.code);
    }

    this.dialogRef.close();
  }
}
