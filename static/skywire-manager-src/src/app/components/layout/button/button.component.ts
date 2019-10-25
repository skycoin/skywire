import { Component, EventEmitter, Input, Output, ViewChild } from '@angular/core';
import { MatButton } from '@angular/material';

enum BUTTON_STATE {
  NORMAL, SUCCESS, ERROR, LOADING
}

@Component({
  selector: 'app-button',
  templateUrl: './button.component.html',
  styleUrls: ['./button.component.scss']
})
export class ButtonComponent {
  @ViewChild('button1') button1: MatButton;
  @ViewChild('button2') button2: MatButton;

  @Input() type = 'mat-button';
  @Input() disabled = false;
  @Input() icon = null;
  @Input() dark = false;
  @Input() color = '';
  @Input() loadingSize = 24;
  @Output() action = new EventEmitter();
  tooltip = '';
  notification = false;
  state = BUTTON_STATE.NORMAL;
  buttonStates = BUTTON_STATE;

  private readonly timeout = 3000;

  click() {
    if (!this.disabled) {
      this.reset();
      this.action.emit();
    }
  }

  reset() {
    this.state = BUTTON_STATE.NORMAL;
    this.tooltip = '';
    this.disabled = false;
    this.notification = false;
  }

  focus() {
    if (this.button1) {
      this.button1.focus();
    }
    if (this.button2) {
      this.button2.focus();
    }
  }

  enable() {
    this.disabled = false;
  }

  disable() {
    this.disabled = true;
  }

  loading() {
    this.state = BUTTON_STATE.LOADING;
    this.disabled = true;
  }

  success() {
    this.state = BUTTON_STATE.SUCCESS;

    setTimeout(() => this.state = BUTTON_STATE.NORMAL, this.timeout);
  }

  error(error: string) {
    this.state = BUTTON_STATE.ERROR;
    this.tooltip = error;
    this.disabled = false;
  }

  notify(notification: boolean) {
    this.notification = notification;
  }
}
