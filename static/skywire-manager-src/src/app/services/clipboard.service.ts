import { Inject } from '@angular/core';
import { Injectable } from '@angular/core';
import { DOCUMENT } from '@angular/common';

/**
 * Allows to copy text to the clipboard.
 */
@Injectable()
export class ClipboardService {

  private dom: Document;

  constructor( @Inject( DOCUMENT ) dom: Document ) {
    this.dom = dom;
  }

  /**
   * Copies text in the clipboard. Returns false in case of error.
   */
  public copy(value: string): boolean {
    let textarea = null;
    let result = false;

    try {
      textarea = this.dom.createElement( 'textarea' );
      textarea.style.height = '0px';
      textarea.style.left = '-100px';
      textarea.style.opacity = '0';
      textarea.style.position = 'fixed';
      textarea.style.top = '-100px';
      textarea.style.width = '0px';
      this.dom.body.appendChild( textarea );

      textarea.value = value;
      textarea.select();

      this.dom.execCommand( 'copy' );

      result = true;
    } finally {
      if ( textarea && textarea.parentNode ) {
        textarea.parentNode.removeChild( textarea );
      }
    }

    return result;
  }
}
