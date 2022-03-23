import { Injectable } from '@angular/core';
import { TranslateService, LangChangeEvent } from '@ngx-translate/core';
import { ReplaySubject } from 'rxjs';

import { AppConfig } from '../app.config';
import { environment } from '../../environments/environment';

export class LanguageData {
  code: string;
  name: string;
  iconName: string;

  constructor(langObj) {
    Object.assign(this, langObj);
  }
}

/**
 * Manages the language displayed in the UI.
 */
@Injectable({
  providedIn: 'root'
})
export class LanguageService {
  currentLanguage = new ReplaySubject<LanguageData>(1);
  languages = new ReplaySubject<LanguageData[]>(1);

  private readonly storageKey = 'lang';
  private languagesInternal: LanguageData[] = [];
  private settingsLoaded = false;

  constructor(
    private translate: TranslateService,
  ) { }

  /**
   * Initializes the language management. Must be called at the start of the app.
   */
  loadLanguageSettings() {
    if (this.settingsLoaded) {
      return;
    }
    this.settingsLoaded = true;

    // Get the available languages from the configuration file.
    const langs: string[] = [];
    AppConfig.languages.forEach(lang => {
      const LangObj = new LanguageData(lang);
      this.languagesInternal.push(LangObj);
      langs.push(LangObj.code);
    });

    // Inform what the currently available languages are.
    this.languages.next(this.languagesInternal);

    // Config Ngx-Translate.
    this.translate.addLangs(langs);
    this.translate.setDefaultLang(AppConfig.defaultLanguage);

    // Detect when the selected language is changed.
    this.translate.onLangChange
      .subscribe((event: LangChangeEvent) => this.onLanguageChanged(event));

    // Load the lastest language selected by the user.
    this.loadCurrentLanguage();
  }

  /**
   * Changes the current language of the UI.
   */
  changeLanguage(langCode: string) {
    this.translate.use(langCode);
  }

  private onLanguageChanged(event: LangChangeEvent) {
    // Inform the changes to the subscribers.
    this.currentLanguage.next(this.languagesInternal.find(val => val.code === event.lang));

    // Save the new selection.
    localStorage.setItem(this.storageKey, event.lang);
  }

  /**
   * Makes the UI to use the lastest language selected by the user.
   */
  private loadCurrentLanguage() {
    let currentLang = localStorage.getItem(this.storageKey);
    currentLang = currentLang ? currentLang : AppConfig.defaultLanguage;

    setTimeout(() => this.translate.use(currentLang), 16);
  }
}
