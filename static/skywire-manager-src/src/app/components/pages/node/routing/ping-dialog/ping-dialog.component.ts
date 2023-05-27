import { Component, OnInit, ViewChild, OnDestroy, ElementRef } from '@angular/core';
import { UntypedFormBuilder, UntypedFormGroup, Validators } from '@angular/forms';
import { MatDialogRef, MatDialog, MatDialogConfig } from '@angular/material/dialog';
import { Subscription } from 'rxjs';

import { AppConfig } from 'src/app/app.config';
import { processServiceError } from 'src/app/utils/errors';
import { NodeService } from 'src/app/services/node.service';
import { ButtonComponent } from 'src/app/components/layout/button/button.component';
import { SnackbarService } from 'src/app/services/snackbar.service';
import { NodeComponent } from '../../node.component';

/**
 * Modal for makinbg a ping request to a remote node.
 */
@Component({
  selector: 'app-ping-dialog-transport',
  templateUrl: './ping-dialog.component.html',
  styleUrls: ['./ping-dialog.component.scss']
})
export class PingDialogComponent implements OnInit, OnDestroy {
  @ViewChild('button') button: ButtonComponent;
  @ViewChild('firstInput') firstInput: ElementRef;
  form: UntypedFormGroup;

  // If the ping operation is being made.
  checking = false;
  // Last result obtained in ms. If null, the form must be shown.
  result: number = null;

  private operationSubscription: Subscription;

  /**
   * Opens the modal window. Please use this function instead of opening the window "by hand".
   */
  public static openDialog(dialog: MatDialog): MatDialogRef<PingDialogComponent, any> {
    const config = new MatDialogConfig();
    config.autoFocus = false;
    config.width = AppConfig.mediumModalWidth;

    return dialog.open(PingDialogComponent, config);
  }

  constructor(
    private formBuilder: UntypedFormBuilder,
    public dialogRef: MatDialogRef<PingDialogComponent>,
    private snackbarService: SnackbarService,
    private nodeService: NodeService,
  ) { }

  ngOnInit() {
    this.form = this.formBuilder.group({
      remoteKey: ['', Validators.compose([
        Validators.required,
        Validators.minLength(66),
        Validators.maxLength(66),
        Validators.pattern('^[0-9a-fA-F]+$')])
      ]
    });
  }

  ngOnDestroy() {
    if (this.operationSubscription) {
      this.operationSubscription.unsubscribe();
    }
  }

  /**
   * If true, all the ways provided by default by the UI for closing the modal window are disabled.
   */
  get disableDismiss(): boolean {
    return this.checking;
  }

  process() {
    if (this.result === null) {
      // Make the request.
      this.ping();
    } else {
      // Show the form again.
      this.checking = false;
      this.result = null;
    }
  }

  /**
   * Makes the ping request.
   */
  ping() {
    if (!this.form.valid || this.button.disabled) {
      return;
    }

    this.checking = true;

    const remotePk: string = this.form.get('remoteKey').value;

    this.operationSubscription = this.nodeService.ping(NodeComponent.getCurrentNodeKey(), remotePk).subscribe(response => {
      this.result = response[0];
      this.checking = false;
    }, err => {
      err = processServiceError(err);
      this.snackbarService.showError(err);
      this.checking = false;
    });
  }
}
