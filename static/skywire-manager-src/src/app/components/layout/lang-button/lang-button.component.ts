import { Component, Input, OnDestroy, OnInit, HostBinding } from '@angular/core';
import { LanguageService, LanguageData } from '../../../services/language.service';
import { Subscription } from 'rxjs';
import { MatDialogConfig, MatDialog } from '@angular/material';
import { SelectLanguageComponent } from '../select-language/select-language.component';

@Component({
  selector: 'app-lang-button',
  templateUrl: './lang-button.component.html',
  styleUrls: ['./lang-button.component.scss']
})
export class LangButtonComponent implements OnInit, OnDestroy {
  @HostBinding('class') get class() { return this.hide ? 'd-none' : ''; }
  language: LanguageData;

  private subscriptionsGroup: Subscription[] = [];
  private hide = true;

  constructor(
    private languageService: LanguageService,
    private dialog: MatDialog,
  ) { }

  ngOnInit() {
    this.subscriptionsGroup.push(this.languageService.currentLanguage.subscribe(lang => {
      this.language = lang;
    }));

    this.subscriptionsGroup.push(this.languageService.languages.subscribe(langs => {
      if (langs.length > 1) {
        this.hide = false;
      } else {
        this.hide = true;
      }
    }));
  }

  ngOnDestroy() {
    this.subscriptionsGroup.forEach(sub => sub.unsubscribe());
  }

  openLanguageWindow() {
    const config = new MatDialogConfig();
    config.autoFocus = false;
    config.width = '600px';
    this.dialog.open(SelectLanguageComponent, config);
  }
}
