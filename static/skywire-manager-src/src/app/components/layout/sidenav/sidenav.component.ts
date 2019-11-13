import {
  Component,
  OnInit,
  HostBinding,
  OnDestroy
} from '@angular/core';
import { SidenavService } from '../../../services/sidenav.service';
import { LanguageService, LanguageData } from 'src/app/services/language.service';
import { Subscription } from 'rxjs';
import { MatDialogConfig, MatDialog } from '@angular/material/dialog';
import { SelectLanguageComponent } from '../select-language/select-language.component';

@Component({
  selector: 'app-sidenav',
  templateUrl: './sidenav.component.html',
  styleUrls: ['./sidenav.component.scss']
})
export class SidenavComponent implements OnInit, OnDestroy {
  language: LanguageData;
  hideLanguageButton = true;

  private langSubscriptionsGroup: Subscription[] = [];

  @HostBinding('class') get class() { return 'full-height flex-column'; }

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
    const config = new MatDialogConfig();
    config.autoFocus = false;
    config.width = '600px';
    this.dialog.open(SelectLanguageComponent, config);
  }
}
