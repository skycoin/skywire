import { Component } from '@angular/core';
import { TranslateService } from '@ngx-translate/core';
import {StorageService} from './services/storage.service';
import {getLangs} from './utils/languageUtils';
import { Router, NavigationEnd } from '@angular/router';
import { Location } from '@angular/common';
import { SnackbarService } from './services/snackbar.service';
import { MatDialog } from '@angular/material/dialog';
import { LanguageService } from './services/language.service';

@Component({
  selector: 'app-root',
  templateUrl: './app.component.html',
  styleUrls: ['./app.component.scss']
})
export class AppComponent {
  constructor(
    private translate: TranslateService,
    private storage: StorageService,
    private location: Location,
    private router: Router,
    snackbarService: SnackbarService,
    dialog: MatDialog,
    languageService: LanguageService,
  ) {
    translate.addLangs(getLangs());
    translate.use(storage.getDefaultLanguage());
    translate.onDefaultLangChange.subscribe(({lang}) => storage.setDefaultLanguage(lang));

    location.subscribe(() => {
      snackbarService.closeCurrent();
      dialog.closeAll();
    });
    dialog.afterOpened.subscribe(() => snackbarService.closeCurrent());

    router.events.subscribe(e => {
      if (e instanceof NavigationEnd) {
        window.scrollTo(0, 0);
      }
    });

    languageService.loadLanguageSettings();
  }
}
