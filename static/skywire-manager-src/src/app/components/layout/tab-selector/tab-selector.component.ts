import { Component, EventEmitter, Input, OnDestroy, Output } from '@angular/core';
import { SelectOptionComponent, SelectableOption } from '../select-option/select-option.component';
import { MatDialog } from '@angular/material/dialog';

/**
 * Button for changing the selected tab of an app-tab-selector component in small screens.
 */
@Component({
  selector: 'app-tab-selector',
  templateUrl: './tab-selector.component.html',
  styleUrls: ['./tab-selector.component.scss']
})
export class TabSelectorComponent implements OnDestroy {
  // Name of the available tabs, for the translation pipe.
  @Input() tabNames: string[] = [''];
  // Index of the currently selected tab.
  @Input() selectedTab = 0;
  // Event emited if the user selects a different tab. The selectedTab var is not
  // updated automatically when this event is sent.
  @Output() tabChanged = new EventEmitter<number>();

  constructor(
    private dialog: MatDialog
  ) { }

  ngOnDestroy() {
    this.tabChanged.complete();
  }

  showTabSelector() {
    const options: SelectableOption[] = [];

    // Create a list with all the tabs.
    this.tabNames.forEach((name, i) => {
      options.push({ icon: i === this.selectedTab ? 'check' : '', label: name })
    });

    // Show the tab selection modal window.
    SelectOptionComponent.openDialog(this.dialog, options, 'node.logs.filter-title').afterClosed().subscribe((selectedOption: number) => {
      this.tabChanged.emit(selectedOption - 1);
    });
  }
}
