import { Injectable } from '@angular/core';
import { Subject } from 'rxjs';

/** Lets the top bar hint open the command palette. */
@Injectable({
  providedIn: 'root'
})
export class CommandPaletteService {
  readonly openRequests = new Subject<void>();

  open() {
    this.openRequests.next();
  }
}

/** True on macOS and iOS, where Cmd-K opens the palette. */
export function paletteIsMac(): boolean {
  return typeof navigator !== 'undefined' && /Mac|iPhone|iPad/.test(navigator.platform || navigator.userAgent || '');
}
