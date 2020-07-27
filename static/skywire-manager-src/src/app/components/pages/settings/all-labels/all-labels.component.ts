import { Component, OnInit, OnDestroy } from '@angular/core';
import { Subscription } from 'rxjs';
import { Router } from '@angular/router';

import { TabButtonData } from '../../../layout/tab-bar/tab-bar.component';
import { SidenavService } from 'src/app/services/sidenav.service';

/**
 * Page for showing the complete list of the labels.
 */
@Component({
  selector: 'app-all-labels',
  templateUrl: './all-labels.component.html',
  styleUrls: ['./all-labels.component.scss']
})
export class AllLabelsComponent implements OnInit, OnDestroy {
  tabsData: TabButtonData[] = [];

  private menuSubscription: Subscription;

  constructor(
    private sidenavService: SidenavService,
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

  ngOnInit() {
    setTimeout(() => {
      // Populate the left options bar.
      this.menuSubscription = this.sidenavService.setContents(null, [
        {
          name: 'settings.title',
          actionName: 'back',
          icon: 'chevron_left'
        }
      ]).subscribe(actionName => {
          // React to the events of the left options bar.
          if (actionName === 'back') {
            this.router.navigate(['settings']);
          }
        }
      );
    });
  }

  ngOnDestroy() {
    if (this.menuSubscription) {
      this.menuSubscription.unsubscribe();
    }
  }
}
