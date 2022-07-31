import { Component, HostListener, Input } from '@angular/core';
import { MatDialog, MatDialogRef } from '@angular/material/dialog';

/**
 * Base component for all the modal windows. Its main function is to show the title bar.
 */
@Component({
  selector: 'app-dialog',
  templateUrl: './dialog.component.html',
  styleUrls: ['./dialog.component.scss']
})
export class DialogComponent {
  @Input() headline: string;
  /**
   * Disables all the ways provided by default by the UI for closing the modal window.
   */
  @Input() disableDismiss: boolean;
  /**
   * If true, this control adds the contents of the modal window inside a scrollable container.
   * If false, the contents must include its own scrollable container.
   */
  @Input() includeScrollableArea = true;
  /**
   * If true, vertical margins will be added to the content.
   */
  @Input() includeVerticalMargins = true;

  // MatDialogRef of the modal window component which is using this component for wrapping
  // the contents.
  private dialogInternal: MatDialogRef<any>;
  @Input() set dialog(val: MatDialogRef<any>) {
    val.disableClose = true;
    this.dialogInternal = val;
  }

  constructor(
    private matDialog: MatDialog,
  ) { }

  @HostListener('window:keyup.esc')
  onKeyUp() {
    this.closePopup();
  }

  closePopup() {
    if (!this.disableDismiss) {
      // Continue only if the current modal window is the topmost one.
      if (this.matDialog.openDialogs[this.matDialog.openDialogs.length - 1].id === this.dialogInternal.id) {
        this.dialogInternal.close();
      }
    }
  }
}
