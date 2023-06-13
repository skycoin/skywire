import { Component, OnInit, ViewChild, OnDestroy, ElementRef } from '@angular/core';
import { UntypedFormBuilder, UntypedFormGroup, Validators } from '@angular/forms';
import { MatDialogRef, MatDialog, MatDialogConfig } from '@angular/material/dialog';
import { Subscription } from 'rxjs';

import { ButtonComponent } from '../../../../../layout/button/button.component';
import { NodeComponent } from '../../../node.component';
import { SnackbarService } from '../../../../../../services/snackbar.service';
import { AppConfig } from 'src/app/app.config';
import { processServiceError } from 'src/app/utils/errors';
import { FwdService } from 'src/app/services/fwd.service';

/**
 * Modal window used for connecting to a remote port. It creates the connection to the remote port
 * and shows a confirmation msg by itself.
 */
@Component({
  selector: 'app-create-remote-rev-port',
  templateUrl: './create-remote-rev-port.component.html',
  styleUrls: ['./create-remote-rev-port.component.scss']
})
export class CreateRemoteRevPortComponent implements OnInit, OnDestroy {
  @ViewChild('button') button: ButtonComponent;
  @ViewChild('firstInput') firstInput: ElementRef;
  form: UntypedFormGroup;

  private operationSubscription: Subscription;

  /**
   * Opens the modal window. Please use this function instead of opening the window "by hand".
   */
  public static openDialog(dialog: MatDialog): MatDialogRef<CreateRemoteRevPortComponent, any> {
    const config = new MatDialogConfig();
    config.autoFocus = false;
    config.width = AppConfig.mediumModalWidth;

    return dialog.open(CreateRemoteRevPortComponent, config);
  }

  constructor(
    private fwdService: FwdService,
    private formBuilder: UntypedFormBuilder,
    public dialogRef: MatDialogRef<CreateRemoteRevPortComponent>,
    private snackbarService: SnackbarService,
  ) { }

  ngOnInit() {
    this.form = this.formBuilder.group({
      remoteKey: ['', Validators.compose([
        Validators.required,
        Validators.minLength(66),
        Validators.maxLength(66),
        Validators.pattern('^[0-9a-fA-F]+$')])
      ],
      remotePort: ['', Validators.compose([Validators.required, Validators.min(1025), Validators.max(65536)])],
      localPort: ['', Validators.compose([Validators.required, Validators.min(1025), Validators.max(65536)])],
    });

    setTimeout(() => (this.firstInput.nativeElement as HTMLElement).focus());
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
    return this.button ? this.button.isLoading : false;
  }

  /**
   * Creates the connection.
   */
  create() {
    if (!this.form.valid || this.button.disabled) {
      return;
    }

    this.button.showLoading();

    const remotePk: string = this.form.get('remoteKey').value;
    const remotePort: number = this.form.get('remotePort').value;
    const localPort: number = this.form.get('localPort').value;

    this.operationSubscription = this.fwdService.createRemote(
      // The node pk is obtained from the currently openned node page.
      NodeComponent.getCurrentNodeKey(),
      remotePk,
      remotePort,
      localPort
    ).subscribe(response => {
      NodeComponent.refreshCurrentDisplayedData();
      this.dialogRef.close();

      this.snackbarService.showDone('remote-rev-ports.dialog.success');
    }, err => {
      this.button.showError();
      err = processServiceError(err);

      this.snackbarService.showError(err);
    });
  }
}
