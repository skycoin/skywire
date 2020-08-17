import { Component } from '@angular/core';
import { Router } from '@angular/router';

import { TabButtonData } from '../../../layout/top-bar/top-bar.component';

/**
 * Page for showing the complete list of the labels.
 */
@Component({
  selector: 'app-all-labels',
  templateUrl: './all-labels.component.html',
  styleUrls: ['./all-labels.component.scss']
})
export class AllLabelsComponent {
  tabsData: TabButtonData[] = [];
  returnButtonText = 'settings.title';

  constructor(
    private router: Router,
  ) {
    // Data for populating the tab bar.
    this.tabsData = [
      {
        icon: 'view_headline',
        label: 'labels.list-title',
        linkParts: [],
      }
    ];
  }

  /**
   * Called when an option form the top bar is selected.
   * @param actionName Name of the selected option.
   */
  performAction(actionName: string) {
    // Null is returned if the back button was pressed.
    if (actionName === null) {
      this.router.navigate(['settings']);
    }
  }
}
