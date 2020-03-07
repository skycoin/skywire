// NOTE: currently not used due to problems connecting to the web socket from some URL. May be
// used in the future.

import { Component, Inject } from '@angular/core';
import { MAT_DIALOG_DATA, MatDialogRef, MatDialog, MatDialogConfig } from '@angular/material/dialog';

import { DomSanitizer } from '@angular/platform-browser';

/**
 * Data needed for TerminalComponent to work.
 */
export interface TerminalData {
  /**
   * Public key of the node.
   */
  pk: string;
  /**
   * Node label.
   */
  label: string;
}

/**
 * Modal window used to show the dmsgpty-ui terminal from the /pty route of the hypervisor.
 */
@Component({
  selector: 'app-terminal',
  templateUrl: './terminal.component.html',
  styleUrls: ['./terminal.component.scss']
})
export class TerminalComponent {
  consoleUrl: any;

  /**
   * Opens the modal window. Please use this function instead of opening the window "by hand".
   */
  public static openDialog(dialog: MatDialog, data: TerminalData): MatDialogRef<TerminalComponent, any> {
    const config = new MatDialogConfig();
    config.data = data;
    config.autoFocus = false;
    config.width = '950px';

    return dialog.open(TerminalComponent, config);
  }

  constructor(
    @Inject(MAT_DIALOG_DATA) public data: TerminalData,
    sanitizer: DomSanitizer,
  ) {
    const protocol = location.protocol;
    const hostname = window.location.host.replace('localhost:4200', '127.0.0.1:8080');

    // Calculate the URL of the dmsgpty-ui terminal.
    this.consoleUrl = sanitizer.bypassSecurityTrustResourceUrl(protocol + '//' + hostname + '/pty/' + data.pk);
  }
}
