import { Component, Inject, Output, EventEmitter, OnDestroy } from '@angular/core';
import { MatDialogRef, MAT_DIALOG_DATA } from '@angular/material';

export interface ConfirmationData {
  text: string;
  headerText: string;
  confirmButtonText: string;
  cancelButtonText?: string;
  disableDismiss?: boolean;
}

enum ConfirmationStates {
  Asking,
  Processing,
  Done,
}

@Component({
  selector: 'app-confirmation',
  templateUrl: './confirmation.component.html',
  styleUrls: ['./confirmation.component.scss'],
})
export class ConfirmationComponent implements OnDestroy {
  disableDismiss = false;
  state = ConfirmationStates.Asking;
  confirmationStates = ConfirmationStates;

  doneTitle: string;
  doneText: string;

  @Output() operationAccepted = new EventEmitter();

  constructor(
    public dialogRef: MatDialogRef<ConfirmationComponent>,
    @Inject(MAT_DIALOG_DATA) public data: ConfirmationData,
  ) {
    this.disableDismiss = !!data.disableDismiss;
    this.dialogRef.disableClose = this.disableDismiss;
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

  showProcessing() {
    this.state = ConfirmationStates.Processing;
  }

  showDone(newTitle: string, newText: string) {
    this.doneTitle = newTitle;
    this.doneText = newText;

    this.state = ConfirmationStates.Done;
    this.dialogRef.disableClose = false;
    this.disableDismiss = false;
  }
}
