import { Component, EventEmitter, Input, Output, ViewChild, OnDestroy } from '@angular/core';
import { MatButton } from '@angular/material/button';

enum ButtonStates {
  Normal, Error, Loading
}

/**
 * Common button used in the app.
 */
@Component({
  selector: 'app-button',
  templateUrl: './button.component.html',
  styleUrls: ['./button.component.scss']
})
export class ButtonComponent implements OnDestroy {
  @ViewChild('button1') button1: MatButton;
  @ViewChild('button2') button2: MatButton;

  // If the button will be in front of the dark background.
  @Input() forDarkBackground = false;
  @Input() disabled = false;
  // Must be one of the colors defined on the default theme.
  @Input() color = '';
  @Input() loadingSize = 24;
  // Click event.
  @Output() action = new EventEmitter();
  state = ButtonStates.Normal;
  buttonStates = ButtonStates;

  ngOnDestroy() {
    this.action.complete();
  }

  click() {
    if (!this.disabled) {
      this.reset();
      this.action.emit();
    }
  }

  reset() {
    this.state = ButtonStates.Normal;
    this.disabled = false;
  }

  focus() {
    if (this.button1) {
      this.button1.focus();
    }
    if (this.button2) {
      this.button2.focus();
    }
  }

  showEnabled() {
    this.disabled = false;
  }

  showDisabled() {
    this.disabled = true;
  }

  showLoading() {
    this.state = ButtonStates.Loading;
    this.disabled = true;
  }

  showError() {
    this.state = ButtonStates.Error;
    this.disabled = false;
  }
}
