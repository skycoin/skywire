import { Component, Input, Output, EventEmitter } from '@angular/core';

export interface TabButtonData {
  linkParts: string[];
  icon: string;
  label: string;
}

@Component({
  selector: 'app-tab-bar',
  templateUrl: './tab-bar.component.html',
  styleUrls: ['./tab-bar.component.scss']
})
export class TabBarComponent {
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

  sendRefreshEvent() {
    this.refreshRequested.emit();
  }
}
