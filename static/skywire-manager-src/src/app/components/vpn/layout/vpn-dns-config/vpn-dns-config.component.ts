import { Component, Inject, ViewChild, ElementRef, OnInit, OnDestroy } from '@angular/core';
import { MAT_DIALOG_DATA, MatDialogRef, MatDialogConfig, MatDialog } from '@angular/material/dialog';
import { FormGroup, FormBuilder, Validators } from '@angular/forms';
import { Subscription } from 'rxjs';

import { SnackbarService } from '../../../../services/snackbar.service';
import { AppConfig } from 'src/app/app.config';
import { ButtonComponent } from 'src/app/components/layout/button/button.component';
import { OperationError } from 'src/app/utils/operation-error';
import { processServiceError } from 'src/app/utils/errors';
import { AppsService } from 'src/app/services/apps.service';
import { VpnClientService } from 'src/app/services/vpn-client.service';
import GeneralUtils from 'src/app/utils/generalUtils';

/**
 * Params for VpnDnsConfigComponent.
 */
export interface VpnDnsConfigParams {
  /**
   * PK of the node.
   */
  nodePk: string;
  /**
   * Current value of the dns property in the app.
   */
   ip: string;
}

/**
 * Modal window for changing the dns configuration of the vpn client app. It changes the values
 * and shows a confirmation msg by itself.
 */
@Component({
  selector: 'app-vpn-dns-config',
  templateUrl: './vpn-dns-config.component.html',
  styleUrls: ['./vpn-dns-config.component.scss']
})
export class VpnDnsConfigComponent implements OnInit, OnDestroy {
  @ViewChild('button') button: ButtonComponent;
  @ViewChild('firstInput') firstInput: ElementRef;

  form: FormGroup;

  private operationSubscription: Subscription;

  /**
   * Opens the modal window. Please use this function instead of opening the window "by hand".
   */
  public static openDialog(dialog: MatDialog, node: VpnDnsConfigParams): MatDialogRef<VpnDnsConfigComponent, any> {
    const config = new MatDialogConfig();
    config.data = node;
    config.autoFocus = false;
    config.width = AppConfig.smallModalWidth;

    return dialog.open(VpnDnsConfigComponent, config);
  }

  constructor(
    private dialogRef: MatDialogRef<VpnDnsConfigComponent>,
    @Inject(MAT_DIALOG_DATA) private data: VpnDnsConfigParams,
    private formBuilder: FormBuilder,
    private snackbarService: SnackbarService,
    private appsService: AppsService,
    private vpnClientService: VpnClientService,
  ) { }

  ngOnInit() {
    this.form = this.formBuilder.group({
      ip: [this.data.ip, Validators.compose([
        Validators.maxLength(15),
        this.validateIp.bind(this)
      ])],
    });

    setTimeout(() => (this.firstInput.nativeElement as HTMLElement).focus());
  }

  ngOnDestroy() {
    if (this.operationSubscription) {
      this.operationSubscription.unsubscribe();
    }
  }

  private validateIp() {
    if (this.form) {
      const value = this.form.get('ip').value as string;
      const validOrEmpty = GeneralUtils.checkIfIpValidOrEmpty(value);

      return validOrEmpty ? null : { invalid: true };
    }

    return null;
  }

  save() {
    if (!this.form.valid || this.operationSubscription) {
      return;
    }

    this.button.showLoading();

    this.operationSubscription = this.appsService.changeAppSettings(
      this.data.nodePk,
      this.vpnClientService.vpnClientAppName,
      { dns: this.form.get('ip').value },
    ).subscribe({
      next: this.onSuccess.bind(this),
      error: this.onError.bind(this)
    });
  }

  private onSuccess(response: any) {
    this.dialogRef.close(true);
    this.snackbarService.showDone('vpn.dns-config.done');
  }

  private onError(err: OperationError) {
    this.button.showError();
    this.operationSubscription = null;
    err = processServiceError(err);

    this.snackbarService.showError(err);
  }
}
