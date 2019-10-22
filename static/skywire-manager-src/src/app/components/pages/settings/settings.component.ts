import { Component } from '@angular/core';
import { Location } from '@angular/common';
import { TabButtonData } from '../../layout/tab-bar/tab-bar.component';

@Component({
  selector: 'app-settings',
  templateUrl: './settings.component.html',
  styleUrls: ['./settings.component.scss']
})
export class SettingsComponent {
  tabsData: TabButtonData[] = [];

  constructor(
    private location: Location,
  ) {
    this.tabsData = [
      {
        icon: 'view_headline',
        label: 'nodes.title',
        linkParts: ['/nodes'],
      },
      {
        icon: 'settings',
        label: 'settings.title',
        linkParts: ['/settings'],
      }
    ];
  }

  back() {
    this.location.back();
  }
}
