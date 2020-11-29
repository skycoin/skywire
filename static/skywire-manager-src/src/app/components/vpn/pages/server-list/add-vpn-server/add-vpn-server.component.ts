import { Component, ViewChild, ElementRef, Inject, OnInit } from '@angular/core';
import { FormBuilder, FormGroup, Validators } from '@angular/forms';
import { MatDialogRef, MatDialog, MatDialogConfig, MAT_DIALOG_DATA } from '@angular/material/dialog';
import { Router } from '@angular/router';

import { AppConfig } from 'src/app/app.config';
import { VpnHelpers } from '../../../vpn-helpers';
import { VpnClientService } from 'src/app/services/vpn-client.service';
import { SnackbarService } from 'src/app/services/snackbar.service';

@Component({
  selector: 'app-add-vpn-server',
  templateUrl: './add-vpn-server.component.html',
  styleUrls: ['./add-vpn-server.component.scss']
})
export class AddVpnServerComponent implements OnInit {
  @ViewChild('firstInput') firstInput: ElementRef;
  form: FormGroup;

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
    private snackbarService: SnackbarService,
  ) { }

  ngOnInit() {
    this.form = this.formBuilder.group({
      'pk': ['', Validators.compose([
        Validators.required,
        Validators.minLength(66),
        Validators.maxLength(66),
        Validators.pattern('^[0-9a-fA-F]+$')])
      ],
      'password': [''],
    });

    setTimeout(() => (this.firstInput.nativeElement as HTMLElement).focus());
  }

  process() {
    if (!this.form.valid) {
      return;
    }

    VpnHelpers.processServerChange(
      this.router,
      this.vpnClientService,
      this.snackbarService,
      this.dialog,
      this.dialogRef,
      this.data,
      this.form.get('pk').value,
      this.form.get('password').value,
    );
  }
}
