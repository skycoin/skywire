import { Component, OnInit, ViewChild, OnDestroy, ElementRef, Inject } from '@angular/core';
import { UntypedFormBuilder, UntypedFormGroup, AbstractControl, ValidationErrors } from '@angular/forms';
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
    styleUrls: ['./skysocks-settings.component.scss'],
    standalone: false
})
export class SkysocksSettingsComponent implements OnInit, OnDestroy {
  @ViewChild('button') button: ButtonComponent;
  @ViewChild('firstInput') firstInput: ElementRef;
  form: UntypedFormGroup;

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
    private formBuilder: UntypedFormBuilder,
    public dialogRef: MatDialogRef<SkysocksSettingsComponent>,
    private snackbarService: SnackbarService,
    private dialog: MatDialog,
  ) {
    if (data.name.toLocaleLowerCase().indexOf('vpn') !== -1) {
      this.configuringVpn = true;
    }
  }

  ngOnInit() {
    this.form = this.formBuilder.group({
      whitelist: ['', this.validateWhitelist.bind(this)],
      netifc: [''],
    });

    // Pre-fill whitelist + netifc from the running app's arg list.
    // The app's whitelist arg is "--whitelist <csv>" — surfaced here
    // as a textarea so users can edit and re-submit.
    if (this.data.args && this.data.args.length > 0) {
      for (let i = 0; i < this.data.args.length; i++) {
        const arg = (this.data.args[i] as string) || '';
        const lower = arg.toLowerCase();
        if (lower.includes('-secure')) {
          this.secureMode = lower.includes('true');
        }
        if (lower.includes('-netifc') && i < this.data.args.length - 1) {
          this.form.get('netifc').setValue(this.data.args[i + 1]);
        }
        if (lower.includes('-whitelist') && i < this.data.args.length - 1) {
          this.form.get('whitelist').setValue(this.data.args[i + 1]);
        }
      }
    }

    setTimeout(() => (this.firstInput.nativeElement as HTMLElement).focus());
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

    const wl = this.normalizedWhitelist();
    const confirmationMsg = wl
      ? 'apps.vpn-socks-server-settings.set-whitelist-confirmation'
      : 'apps.vpn-socks-server-settings.clear-whitelist-confirmation';

    const confirmationDialog = GeneralUtils.createConfirmationDialog(this.dialog, confirmationMsg);
    confirmationDialog.componentInstance.operationAccepted.subscribe(() => {
      confirmationDialog.close();
      this.continueSavingChanges();
    });
  }

  private continueSavingChanges() {
    this.button.showLoading();

    const data: any = { whitelist: this.normalizedWhitelist() };
    // The "secure" value and netifc are only for vpn-server.
    if (this.configuringVpn) {
      data.secure = this.secureMode;
      data.netifc = this.form.get('netifc').value;
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

  /**
   * Returns the whitelist value as a comma-separated string of public
   * keys with whitespace squashed. Empty string when the field is
   * blank — this clears the whitelist server-side.
   */
  private normalizedWhitelist(): string {
    const raw = (this.form.get('whitelist').value || '').trim();
    if (raw === '') {
      return '';
    }
    return raw.split(/[\s,]+/).filter((s: string) => s.length > 0).join(',');
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

  private validateWhitelist(control: AbstractControl): ValidationErrors | null {
    const raw = (control && (control.value || '') as string).trim();
    if (raw === '') {
      return null; // empty = clear whitelist
    }
    for (const pk of raw.split(/[\s,]+/)) {
      if (pk === '') { continue; }
      if (pk.length !== 66 || !/^[0-9a-fA-F]+$/.test(pk)) {
        return { invalid: true };
      }
    }
    return null;
  }
}
