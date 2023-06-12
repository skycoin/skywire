import { Component, OnInit, ViewChild, OnDestroy, Inject } from '@angular/core';
import { UntypedFormBuilder, UntypedFormGroup, Validators } from '@angular/forms';
import { MatDialogRef, MatDialog, MatDialogConfig, MAT_DIALOG_DATA } from '@angular/material/dialog';
import { Subscription } from 'rxjs';

import { ButtonComponent } from '../../../../../layout/button/button.component';
import { NodeComponent } from '../../../node.component';
import { SnackbarService } from '../../../../../../services/snackbar.service';
import { AppConfig } from 'src/app/app.config';
import { processServiceError } from 'src/app/utils/errors';
import { OperationError } from 'src/app/utils/operation-error';
import { AppsService } from 'src/app/services/apps.service';
import { Application } from 'src/app/app.datatypes';
import GeneralUtils from 'src/app/utils/generalUtils';

/**
 * Modal window used for configuring the Skychat app.
 */
@Component({
  selector: 'app-skychat-settings',
  templateUrl: './skychat-settings.component.html',
  styleUrls: ['./skychat-settings.component.scss']
})
export class SkychatSettingsComponent implements OnInit, OnDestroy {
  @ViewChild('button') button: ButtonComponent;
  form: UntypedFormGroup;

  private formSubscription: Subscription;
  private operationSubscription: Subscription;

  /**
   * Opens the modal window. Please use this function instead of opening the window "by hand".
   */
  public static openDialog(dialog: MatDialog, app: Application): MatDialogRef<SkychatSettingsComponent, any> {
    const config = new MatDialogConfig();
    config.data = app;
    config.autoFocus = false;
    config.width = AppConfig.mediumModalWidth;

    return dialog.open(SkychatSettingsComponent, config);
  }

  constructor(
    @Inject(MAT_DIALOG_DATA) private data: Application,
    private appsService: AppsService,
    private formBuilder: UntypedFormBuilder,
    public dialogRef: MatDialogRef<SkychatSettingsComponent>,
    private snackbarService: SnackbarService,
    private dialog: MatDialog,
  ) { }

  ngOnInit() {
    this.form = this.formBuilder.group({
      localhostOnly: [true],
      port: ['', Validators.compose([Validators.required, Validators.min(1025), Validators.max(65536)])],
    });

    this.formSubscription = this.form.get('localhostOnly').valueChanges.subscribe(value => {
      // If "no" is selected ask for confirmation.
      if (!value) {
        this.form.get('localhostOnly').setValue(true);
        const confirmationDialog = GeneralUtils.createConfirmationDialog(this.dialog, 'apps.skychat-settings.non-localhost-confirmation');

        confirmationDialog.componentInstance.operationAccepted.subscribe(() => {
          confirmationDialog.componentInstance.closeModal();

          this.form.get('localhostOnly').setValue(false, { emitEvent: false });
        });
      }
    });

    // Get the current values saved on the visor, if returned by the API.
    if (this.data.args && this.data.args.length > 0) {
      for (let i = 0; i < this.data.args.length; i++) {
        if (this.data.args[i] === '-addr' && i + 1 < this.data.args.length) {
          const parts = (this.data.args[i + 1] as string).split(':');
          if (parts[0] === '*') {
            this.form.get('localhostOnly').setValue(false);
          }

          this.form.get('port').setValue(parts[1]);
        }
      }
    }
  }

  ngOnDestroy() {
    if (this.formSubscription) {
      this.formSubscription.unsubscribe();
    }

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
   * Saves the settings.
   */
  saveChanges() {
    if (!this.form.valid || this.button.disabled) {
      return;
    }

    this.button.showLoading();

    const data = {address: this.form.get('localhostOnly').value ? ':' : '*:'};
    data['address'] += this.form.get('port').value;

    this.operationSubscription = this.appsService.changeAppSettings(
      // The node pk is obtained from the currently openned node page.
      NodeComponent.getCurrentNodeKey(),
      this.data.name,
      data,
    ).subscribe({
      next: this.onSuccess.bind(this),
      error: this.onError.bind(this)
    });
  }

  private onSuccess() {
    NodeComponent.refreshCurrentDisplayedData();
    this.snackbarService.showDone('apps.skychat-settings.changes-made');
    this.dialogRef.close();
  }

  private onError(err: OperationError) {
    this.button.showError();
    err = processServiceError(err);

    this.snackbarService.showError(err);
  }
}
