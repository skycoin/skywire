import { Directive, Output, Input, HostListener, OnDestroy } from '@angular/core';
import { EventEmitter } from '@angular/core';

import {ClipboardService} from '../services/clipboard.service';

/**
 * Makes a component copy a specific text to the clipboard when clicked.
 */
@Directive({
     
    selector: '[appClipboard]',
    standalone: false
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
   
  @Input() appClipboard: string;

  constructor(private clipboardService: ClipboardService) {
    this.copyEvent = new EventEmitter();
    this.errorEvent = new EventEmitter();
    this.appClipboard = '';
  }

  ngOnDestroy() {
    this.copyEvent.complete();
    this.errorEvent.complete();
  }

  @HostListener('click') copyToClipboard(): void {
    // Use ClipboardService to copy the text.
    if (this.clipboardService.copy(this.appClipboard)) {
      this.copyEvent.emit(this.appClipboard);
    } else {
      this.errorEvent.emit();
    }
  }
}
