import {
  Component,
  OnInit,
  OnDestroy
} from '@angular/core';
import { LanguageService, LanguageData } from 'src/app/services/language.service';
import { Subscription } from 'rxjs';
import { MatDialog } from '@angular/material/dialog';

import { SidenavService } from '../../../services/sidenav.service';
import { SelectLanguageComponent } from '../select-language/select-language.component';

/**
 * Base component for the pages that show an options bar at the left. It shows the options bar
 * at the left on big screens and a top bar (with an extra button for changing the language) on
 * small screens. It acts as a container for the content of the page. It works interacting
 * with SidenavService.
 */
@Component({
  selector: 'app-sidenav',
  templateUrl: './sidenav.component.html',
  styleUrls: ['./sidenav.component.scss']
})
export class SidenavComponent implements OnInit, OnDestroy {
  language: LanguageData;
  // The language button is only shown if there is more than one language.
  hideLanguageButton = true;

  private langSubscriptionsGroup: Subscription[] = [];

  constructor(
    public sidenavService: SidenavService,
    private languageService: LanguageService,
    private dialog: MatDialog,
  ) { }

  ngOnInit() {
    this.langSubscriptionsGroup.push(this.languageService.currentLanguage.subscribe(lang => {
      this.language = lang;
    }));

    this.langSubscriptionsGroup.push(this.languageService.languages.subscribe(langs => {
      if (langs.length > 1) {
        this.hideLanguageButton = false;
      } else {
        this.hideLanguageButton = true;
      }
    }));
  }

  ngOnDestroy() {
    this.langSubscriptionsGroup.forEach(sub => sub.unsubscribe());
  }

  requestAction(name: string) {
    this.sidenavService.requestAction(name);
  }

  openLanguageWindow() {
    SelectLanguageComponent.openDialog(this.dialog);
  }
}
