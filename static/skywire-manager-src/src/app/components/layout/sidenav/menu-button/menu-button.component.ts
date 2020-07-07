import { Component, EventEmitter, Input, Output, OnDestroy } from '@angular/core';

/**
 * Buttons shown in the left menu.
 */
@Component({
  selector: 'app-menu-button',
  templateUrl: './menu-button.component.html',
  styleUrls: ['./menu-button.component.scss']
})
export class MenuButtonComponent implements OnDestroy {
  @Input() disabled = false;
  @Input() icon: string;
  @Input() text: string;
  // If true, only the separator at the top will be shown. If false, only the separator at
  // the bottom will be shown.
  @Input() showUpperSeparator = false;
  // Click event.
  @Output() action = new EventEmitter();

  ngOnDestroy() {
    this.action.complete();
  }

  click() {
    if (!this.disabled) {
      this.action.emit();
    }
  }

  showEnabled() {
    this.disabled = false;
  }

  showDisabled() {
    this.disabled = true;
  }
}
