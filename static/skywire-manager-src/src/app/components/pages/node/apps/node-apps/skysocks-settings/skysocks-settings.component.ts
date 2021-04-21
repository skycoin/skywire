import { Component, OnInit, ViewChild, OnDestroy, ElementRef, Inject } from '@angular/core';
import { FormBuilder, FormGroup } from '@angular/forms';
import { MatDialogRef, MatDialog, MatDialogConfig, MAT_DIALOG_DATA } from '@angular/material/dialog';
import { Subscription } from 'rxjs';

import { ButtonComponent } from '../../../../../layout/button/button.component';
import { NodeComponent } from '../../../node.component';
import { SnackbarService } from '../../../../../../services/snackbar.service';
import { AppConfig } from 'src/app/app.config';
import { processServiceError } from 'src/app/utils/errors';
import { OperationError } from 'src/app/utils/operation-error';
import { AppsService } from 'src/app/services/apps.service';
import GeneralUtils from 'src/app/utils/generalUtils';
import { Application } from 'src/app/app.datatypes';

/**
 * Modal window used for configuring the Skysocks and Vpn-Server apps.
 */
@Component({
  selector: 'app-skysocks-settings',
  templateUrl: './skysocks-settings.component.html',
  styleUrls: ['./skysocks-settings.component.scss']
})
export class SkysocksSettingsComponent implements OnInit, OnDestroy {
  @ViewChild('button') button: ButtonComponent;
  @ViewChild('firstInput') firstInput: ElementRef;
  form: FormGroup;

  // True if configuring Vpn-Server, false if configuring Skysocks.
  configuringVpn = false;

  // Indicates if the secure mode option is selected in the UI or not.
  secureMode = false;

  private operationSubscription: Subscription;
  private formSubscription: Subscription;

  /**
   * Opens the modal window. Please use this function instead of opening the window "by hand".
   */
  public static openDialog(dialog: MatDialog, app: Application): MatDialogRef<SkysocksSettingsComponent, any> {
    const config = new MatDialogConfig();
    config.data = app;
    config.autoFocus = false;
    config.width = AppConfig.mediumModalWidth;

    return dialog.open(SkysocksSettingsComponent, config);
  }

  constructor(
    @Inject(MAT_DIALOG_DATA) private data: Application,
    private appsService: AppsService,
    private formBuilder: FormBuilder,
    private dialogRef: MatDialogRef<SkysocksSettingsComponent>,
    private snackbarService: SnackbarService,
    private dialog: MatDialog,
  ) {
    if (data.name.toLocaleLowerCase().indexOf('vpn') !== -1) {
      this.configuringVpn = true;
    }

    // Get the current values saved on the visor, if returned by the API.
    if (this.data.args && this.data.args.length > 0) {
      for (let i = 0; i < this.data.args.length; i++) {
        if ((this.data.args[i] as string).toLowerCase().includes('-secure')) {
          this.secureMode = (this.data.args[i] as string).toLowerCase().includes('true');
        }
      }
    }
  }

  ngOnInit() {
    this.form = this.formBuilder.group({
      'password': [''],
      'passwordConfirmation': ['', this.validatePasswords.bind(this)],
    });

    this.formSubscription = this.form.get('password').valueChanges.subscribe(() => {
      this.form.get('passwordConfirmation').updateValueAndValidity();
    });

    setTimeout(() => (this.firstInput.nativeElement as HTMLElement).focus());
  }

  ngOnDestroy() {
    this.formSubscription.unsubscribe();
    if (this.operationSubscription) {
      this.operationSubscription.unsubscribe();
    }
  }

  // Used by the checkbox for the secure mode setting.
  setSecureMode(event) {
    if (!this.button.disabled) {
      this.secureMode = event.checked ? true : false;
    }
  }

  /**
   * Saves the settings.
   */
  saveChanges() {
    if (!this.form.valid || this.button.disabled) {
      return;
    }

    // Ask for confirmation.

    const confirmationMsg = this.form.get('password').value ?
      'apps.vpn-socks-server-settings.change-passowrd-confirmation' : 'apps.vpn-socks-server-settings.remove-passowrd-confirmation';

    const confirmationDialog = GeneralUtils.createConfirmationDialog(this.dialog, confirmationMsg);
    confirmationDialog.componentInstance.operationAccepted.subscribe(() => {
      confirmationDialog.close();
      this.continueSavingChanges();
    });
  }

  private continueSavingChanges() {
    this.button.showLoading();

    const data = { passcode: this.form.get('password').value };
    // The "secure" value is only for the VPN app.
    if (this.configuringVpn) {
      data['secure'] = this.secureMode;
    }

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
    this.snackbarService.showDone('apps.vpn-socks-server-settings.changes-made');
    this.dialogRef.close();
  }

  private onError(err: OperationError) {
    this.button.showError();
    err = processServiceError(err);

    this.snackbarService.showError(err);
  }

  private validatePasswords() {
    if (this.form) {
      return this.form.get('password').value !== this.form.get('passwordConfirmation').value
        ? { invalid: true } : null;
    } else {
      return null;
    }
  }
}
