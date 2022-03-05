import { Directive, Output, Input, HostListener, OnDestroy } from '@angular/core';
import { EventEmitter } from '@angular/core';

import {ClipboardService} from '../services/clipboard.service';

/**
 * Makes a component copy a specific text to the clipboard when clicked.
 */
@Directive({
  /* eslint-disable @angular-eslint/directive-selector */
  selector: '[clipboard]',
})
export class ClipboardDirective implements OnDestroy {
  /**
   * Event sent when the text is copied.
   */
  @Output() copyEvent: EventEmitter<string>;
  /**
   * Event sent when it was not possible to copy the text.
   */
  @Output() errorEvent: EventEmitter<void>;
  /* eslint-disable @angular-eslint/no-input-rename */
  @Input('clipboard') value: string;

  constructor(private clipboardService: ClipboardService) {
    this.copyEvent = new EventEmitter();
    this.errorEvent = new EventEmitter();
    this.value = '';
  }

  ngOnDestroy() {
    this.copyEvent.complete();
    this.errorEvent.complete();
  }

  @HostListener('click') copyToClipboard(): void {
    // Use ClipboardService to copy the text.
    if (this.clipboardService.copy(this.value)) {
      this.copyEvent.emit(this.value);
    } else {
      this.errorEvent.emit();
    }
  }
}
