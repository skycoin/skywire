import { Component, Input, Output, EventEmitter, OnInit, OnDestroy } from '@angular/core';
import { Subscription } from 'rxjs';
import { MatDialog } from '@angular/material/dialog';
import { Router } from '@angular/router';

import { LanguageService } from 'src/app/services/language.service';
import { SelectableOption, SelectOptionComponent } from '../select-option/select-option.component';

/**
 * Properties of the tabs shown in TabBarComponent.
 */
export interface TabButtonData {
  /**
   * Array with the parts of the route that must be openned by the tab. This array must the
   * same that would be usend in the "routerLink" property of an <a> tag.
   */
  linkParts: string[];
  icon: string;
  label: string;
  /**
   * If true, the button is not visible in the "lg" window size and larger.
   */
  onlyIfLessThanLg?: boolean;
}

/**
 * Tab bar shown by most of the pages. It shows a list of tabs, a button for refreshing the
 * currently displayed data and a language button (only on large screens). The design is
 * responsive, but it is advisable to use only a maximum of 3 tabs with short texts, to
 * avoid some problems.
 */
@Component({
  selector: 'app-tab-bar',
  templateUrl: './tab-bar.component.html',
  styleUrls: ['./tab-bar.component.scss']
})
export class TabBarComponent implements OnInit, OnDestroy {
  /**
   * Deactivates the mouse events.
   */
  @Input() disableMouse = false;

  /**
   * Elements to show in the title. The idea is to show the path of the current page.
   */
  @Input() titleParts: string[];
  /**
   * List with the tabs to show.
   */
  @Input() tabsData: TabButtonData[];
  @Input() selectedTabIndex = 0;

  /**
   * Seconds since the last time the data was updated.
   */
  @Input() secondsSinceLastUpdate: number;
  /**
   * Makes the refresh button to show a loading animation.
   */
  @Input() showLoading: boolean;
  /**
   * Makes the refresh button to show an alert icon, to inform that there was an error
   * updating the data. It also activates a tooltip in which he user can see how often
   * the system retries to get the data.
   */
  @Input() showAlert: boolean;
  /**
   * How often the system automatically refreshes the data, in seconds.
   */
  @Input() refeshRate = -1;
  @Input() showUpdateButton = true;

  /**
   * Event for when the user presses the update button.
   */
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
    this.refreshRequested.complete();
  }

  sendRefreshEvent() {
    this.refreshRequested.emit();
  }

  openTabSelector() {
    // Create an option for every tab.
    const options: SelectableOption[] = [];
    this.tabsData.forEach(tab => {
      options.push({
        label: tab.label,
        icon: tab.icon,
      });
    });

    // Open the option selection modal window.
    SelectOptionComponent.openDialog(this.dialog, options, 'tabs-window.title').afterClosed().subscribe((result: number) => {
      if (result) {
        result -= 1;
        if (result !== this.selectedTabIndex) {
          this.router.navigate(this.tabsData[result].linkParts);
        }
      }
    });
  }
}
