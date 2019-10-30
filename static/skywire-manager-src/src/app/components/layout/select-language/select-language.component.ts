import { Component, OnInit, OnDestroy } from '@angular/core';
import { MatDialogRef } from '@angular/material/dialog';
import { LanguageData, LanguageService } from '../../../services/language.service';
import { Subscription } from 'rxjs';

@Component({
  selector: 'app-select-language',
  templateUrl: './select-language.component.html',
  styleUrls: ['./select-language.component.scss'],
})
export class SelectLanguageComponent implements OnInit, OnDestroy {
  languages: LanguageData[] = [];

  private subscription: Subscription;

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
