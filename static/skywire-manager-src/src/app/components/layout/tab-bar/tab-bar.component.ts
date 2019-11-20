import { Component, Input, Output, EventEmitter, OnInit, OnDestroy } from '@angular/core';
import { LanguageService } from 'src/app/services/language.service';
import { Subscription } from 'rxjs';
import { MatDialog } from '@angular/material/dialog';
import { SelectTabComponent } from './select-tab/select-tab.component';
import { Router } from '@angular/router';

export interface TabButtonData {
  linkParts: string[];
  icon: string;
  label: string;
  notInXl?: boolean;
}

@Component({
  selector: 'app-tab-bar',
  templateUrl: './tab-bar.component.html',
  styleUrls: ['./tab-bar.component.scss']
})
export class TabBarComponent implements OnInit, OnDestroy {
  @Input() disableMouse = false;

  @Input() titleParts: string[];
  @Input() tabsData: TabButtonData[];
  @Input() selectedTabIndex = 0;

  @Input() secondsSinceLastUpdate: number;
  @Input() showLoading: boolean;
  @Input() showAlert: boolean;
  @Input() refeshRate = -1;
  @Input() showUpdateButton = true;

  @Output() refreshRequested = new EventEmitter();

  hideLanguageButton = true;

  private langSubscription: Subscription;

  constructor(
    private languageService: LanguageService,
    private dialog: MatDialog,
    private router: Router,
  ) { }

  ngOnInit() {
    this.langSubscription = this.languageService.languages.subscribe(langs => {
      if (langs.length > 1) {
        this.hideLanguageButton = false;
      } else {
        this.hideLanguageButton = true;
      }
    });
  }

  ngOnDestroy() {
    this.langSubscription.unsubscribe();
  }

  sendRefreshEvent() {
    this.refreshRequested.emit();
  }

  openTabSelector() {
    SelectTabComponent.openDialog(this.dialog, this.tabsData).afterClosed().subscribe((result: number) => {
      if (result) {
        result -= 1;
        if (result !== this.selectedTabIndex) {
          this.router.navigate(this.tabsData[result].linkParts, {replaceUrl: true});
        }
      }
    });
  }
}
