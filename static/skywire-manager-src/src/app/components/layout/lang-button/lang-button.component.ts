import { Component, OnDestroy, OnInit } from '@angular/core';
import { LanguageService, LanguageData } from '../../../services/language.service';
import { Subscription } from 'rxjs';
import { MatDialog } from '@angular/material/dialog';
import { SelectLanguageComponent } from '../select-language/select-language.component';

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
