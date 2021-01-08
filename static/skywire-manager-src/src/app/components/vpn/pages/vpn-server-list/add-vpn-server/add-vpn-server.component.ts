import { Component, ViewChild, ElementRef, Inject, OnInit } from '@angular/core';
import { FormBuilder, FormGroup, Validators } from '@angular/forms';
import { MatDialogRef, MatDialog, MatDialogConfig, MAT_DIALOG_DATA } from '@angular/material/dialog';
import { Router } from '@angular/router';

import { AppConfig } from 'src/app/app.config';
import { VpnHelpers } from '../../../vpn-helpers';
import { VpnClientService } from 'src/app/services/vpn-client.service';
import { SnackbarService } from 'src/app/services/snackbar.service';
import { VpnSavedDataService } from 'src/app/services/vpn-saved-data.service';

/**
 * Data AddVpnServerComponent collets.
 */
export interface ManualVpnServerData {
  pk: string;
  name?: string;
  note?: string;
}

/**
 * Modal window for entering the data of a VPN server manually. If the user confirms the
 * operation, the window saves the new server and start connecting with it.
 */
@Component({
  selector: 'app-add-vpn-server',
  templateUrl: './add-vpn-server.component.html',
  styleUrls: ['./add-vpn-server.component.scss']
})
export class AddVpnServerComponent implements OnInit {
  @ViewChild('firstInput') firstInput: ElementRef;
  form: FormGroup;

  /**
   * Opens the modal window. Please use this function instead of opening the window "by hand".
   */
  public static openDialog(dialog: MatDialog, currentLocalPk: string): MatDialogRef<AddVpnServerComponent, any> {
    const config = new MatDialogConfig();
    config.data = currentLocalPk;
    config.autoFocus = false;
    config.width = AppConfig.mediumModalWidth;

    return dialog.open(AddVpnServerComponent, config);
  }

  constructor(
    private dialogRef: MatDialogRef<AddVpnServerComponent>,
    @Inject(MAT_DIALOG_DATA) private data: string,
    private formBuilder: FormBuilder,
    private dialog: MatDialog,
    private router: Router,
    private vpnClientService: VpnClientService,
    private vpnSavedDataService: VpnSavedDataService,
    private snackbarService: SnackbarService,
  ) { }

  ngOnInit() {
    // Init the form.
    this.form = this.formBuilder.group({
      'pk': ['', Validators.compose([
        Validators.required,
        Validators.minLength(66),
        Validators.maxLength(66),
        Validators.pattern('^[0-9a-fA-F]+$')])
      ],
      'password': [''],
      'name': [''],
      'note': [''],
    });

    setTimeout(() => (this.firstInput.nativeElement as HTMLElement).focus());
  }

  /**
   * Saves the server and starts connecting with it.
   */
  process() {
    if (!this.form.valid) {
      return;
    }

    const serverData: ManualVpnServerData = {
      pk: this.form.get('pk').value,
      name: this.form.get('name').value,
      note: this.form.get('note').value,
    };

    VpnHelpers.processServerChange(
      this.router,
      this.vpnClientService,
      this.vpnSavedDataService,
      this.snackbarService,
      this.dialog,
      this.dialogRef,
      this.data,
      null,
      null,
      serverData,
      this.form.get('password').value
    );
  }
}
