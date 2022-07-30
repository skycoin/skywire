import { Component, Inject, Output, EventEmitter, OnDestroy, ViewChild, AfterViewInit } from '@angular/core';
import { MatDialogRef, MAT_DIALOG_DATA } from '@angular/material/dialog';

import { ButtonComponent } from '../button/button.component';

/**
 * Data that must be pased to ConfirmationComponent.
 */
export interface ConfirmationData {
  /**
   * Confirmation text to show.
   */
  text: string;
  /**
   * Optional list with elements to show after the text.
   */
  list?: string[];
  /**
   * Optional text to show below the main text and the list.
   */
  lowerText?: string;
  /**
   * Title of the modal window.
   */
  headerText: string;
  /**
   * Text for the confirmation button.
   */
  confirmButtonText: string;
  /**
   * Text for the cancel button. the button is not shown if no text is provided.
   */
  cancelButtonText?: string;
  /**
   * If true, in the Asking state the window can only be closed by pressing the cancel
   * button, if present.
   */
  disableDismiss?: boolean;
}

/**
 * States of the modal window:
 * - Asking: for asking confirmation from the user.
 * - Processing: the buttons are disabled, the user can not close the window and a loading
 * indicator is shown.
 * - Done: the window shows a msg (could be for informing success or an error) and a close button
 * is shown.
 */
enum ConfirmationStates {
  Asking = 'Asking',
  Processing = 'Processing',
  Done = 'Done',
}

/**
 * Modal window used to request confirmation from the user. It has 3 posible states, which can be changed
 * via code (the component does not change the state by itself). The initial state is Asking. When the
 * user confirms an event is sent, the window does not close itself.
 */
@Component({
  selector: 'app-confirmation',
  templateUrl: './confirmation.component.html',
  styleUrls: ['./confirmation.component.scss'],
})
export class ConfirmationComponent implements AfterViewInit, OnDestroy {
  @ViewChild('cancelButton') cancelButton: ButtonComponent;
  @ViewChild('confirmButton') confirmButton: ButtonComponent;

  disableDismiss = false;
  state = ConfirmationStates.Asking;
  confirmationStates = ConfirmationStates;

  // Texts for the Done state.
  doneTitle: string;
  doneText: string;
  doneList: string[];

  // Event for when the user confirms.
  @Output() operationAccepted = new EventEmitter();

  constructor(
    public dialogRef: MatDialogRef<ConfirmationComponent>,
    @Inject(MAT_DIALOG_DATA) public data: ConfirmationData,
  ) {
    this.disableDismiss = !!data.disableDismiss;
    this.dialogRef.disableClose = this.disableDismiss;
  }

  ngAfterViewInit() {
    if (this.data.cancelButtonText) {
      setTimeout(() => this.cancelButton.focus());
    } else {
      setTimeout(() => this.confirmButton.focus());
    }
  }

  ngOnDestroy() {
    this.operationAccepted.complete();
  }

  closeModal() {
    this.dialogRef.close();
  }

  sendOperationAcceptedEvent() {
    this.operationAccepted.emit();
  }

  /**
   * Puts the modal window in the state in which it is waiting for confirmation.
   * @param newData New configuration for the modal window.
   */
  showAsking(newData: ConfirmationData | null) {
    if (newData) {
      this.data = newData;
    }

    this.state = ConfirmationStates.Asking;
    this.confirmButton.reset();
    this.disableDismiss = false;
    this.dialogRef.disableClose = this.disableDismiss;

    if (this.cancelButton) {
      this.cancelButton.showEnabled();
    }
  }

  showProcessing() {
    this.state = ConfirmationStates.Processing;
    this.disableDismiss = true;
    this.confirmButton.showLoading();

    if (this.cancelButton) {
      this.cancelButton.showDisabled();
    }
  }

  /**
   * Use only after the operation is done or receiving an error.
   * @param newTitle New title for the modal window.
   * @param newText New main text for the modal window.
   * @param newList New optional list to show below the main text.
   */
  showDone(newTitle: string | null, newText: string, newList: string[] = null) {
    if (newTitle) {
      this.doneTitle = newTitle;
    } else {
      this.doneTitle = this.data.headerText;
    }
    this.doneText = newText;
    this.doneList = newList;

    this.confirmButton.reset();
    setTimeout(() => this.confirmButton.focus());

    this.state = ConfirmationStates.Done;
    this.dialogRef.disableClose = false;
    this.disableDismiss = false;
  }
}
