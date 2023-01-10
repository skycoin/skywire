import { Component, OnDestroy, OnInit } from '@angular/core';
import { Subscription } from 'rxjs';
import { MatLegacyDialog as MatDialog } from '@angular/material/legacy-dialog';

import { LanguageService, LanguageData } from '../../../services/language.service';
import { SelectLanguageComponent } from '../select-language/select-language.component';

/**
 * Button for opening the language selection modal window. It normally is in the tab bar.
 */
@Component({
  selector: 'app-lang-button',
  templateUrl: './lang-button.component.html',
  styleUrls: ['./lang-button.component.scss']
})
export class LangButtonComponent implements OnInit, OnDestroy {
  language: LanguageData;

  private subscription: Subscription;

  constructor(
    private languageService: LanguageService,
    private dialog: MatDialog,
  ) { }

  ngOnInit() {
    this.subscription = this.languageService.currentLanguage.subscribe(lang => {
      this.language = lang;
    });
  }

  ngOnDestroy() {
    this.subscription.unsubscribe();
  }

  openLanguageWindow() {
    SelectLanguageComponent.openDialog(this.dialog);
  }
}
