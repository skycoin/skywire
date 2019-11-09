import { Injectable } from '@angular/core';
import { TranslateService, LangChangeEvent } from '@ngx-translate/core';
import { AppConfig } from '../app.config';
import { ReplaySubject } from 'rxjs';
import { environment } from '../../environments/environment';

export class LanguageData {
  code: string;
  name: string;
  iconName: string;

  constructor(langObj) {
    Object.assign(this, langObj);
  }
}

@Injectable({
  providedIn: 'root'
})
export class LanguageService {
  currentLanguage = new ReplaySubject<LanguageData>(1);
  languages = new ReplaySubject<LanguageData[]>(1);

  private readonly storageKey = 'lang';
  private languagesInternal: LanguageData[] = [];
  private settingLoaded = false;

  constructor(
    private translate: TranslateService,
  ) { }

  loadLanguageSettings() {
    if (this.settingLoaded) {
      return;
    }
    this.settingLoaded = true;

    let langs: string[] = [];
    AppConfig.languages.forEach(lang => {
      const LangObj = new LanguageData(lang);
      this.languagesInternal.push(LangObj);
      langs.push(LangObj.code);
    });

    if (environment.production) {
      this.languagesInternal = this.languagesInternal.filter(lang => lang.code !== 'es');
      langs = langs.filter(lang => lang !== 'es');
    }

    this.languages.next(this.languagesInternal);

    this.translate.addLangs(langs);
    this.translate.setDefaultLang(AppConfig.defaultLanguage);

    this.translate.onLangChange
      .subscribe((event: LangChangeEvent) => this.onLanguageChanged(event));

    this.loadCurrentLanguage();
  }

  changeLanguage(langCode: string) {
    this.translate.use(langCode);
  }

  private onLanguageChanged(event: LangChangeEvent) {
    this.currentLanguage.next(this.languagesInternal.find(val => val.code === event.lang));

    localStorage.setItem(this.storageKey, event.lang);
  }

  private loadCurrentLanguage() {
    const currentLang = localStorage.getItem(this.storageKey);

    setTimeout(() => { this.translate.use(currentLang); }, 16);
  }
}
