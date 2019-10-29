import { Component } from '@angular/core';
import { TranslateService } from '@ngx-translate/core';
import {StorageService} from './services/storage.service';
import {getLangs} from './utils/languageUtils';
import { Router } from '@angular/router';
import { Location } from '@angular/common';
import { SnackbarService } from './services/snackbar.service';
import { MatDialog } from '@angular/material';

@Component({
  selector: 'app-root',
  templateUrl: './app.component.html',
  styleUrls: ['./app.component.scss']
})
export class AppComponent {
  showFooter = false;

  constructor(
    private translate: TranslateService,
    private storage: StorageService,
    private location: Location,
    private router: Router,
    snackbarService: SnackbarService,
    dialog: MatDialog,
  ) {
    translate.addLangs(getLangs());
    translate.use(storage.getDefaultLanguage());
    translate.onDefaultLangChange.subscribe(({lang}) => storage.setDefaultLanguage(lang));

    location.subscribe(() => {
      snackbarService.closeCurrent();
      dialog.closeAll();
    });
    dialog.afterOpen.subscribe(() => snackbarService.closeCurrent());

    router.events.subscribe(() => {
      this.showFooter = !location.isCurrentPathEqualTo('/login');
    });
  }
}
