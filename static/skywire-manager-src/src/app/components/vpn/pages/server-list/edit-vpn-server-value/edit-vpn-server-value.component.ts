import { Component, ViewChild, ElementRef, Inject, OnInit } from '@angular/core';
import { FormBuilder, FormGroup } from '@angular/forms';
import { MatDialogRef, MatDialog, MatDialogConfig, MAT_DIALOG_DATA } from '@angular/material/dialog';

import { AppConfig } from 'src/app/app.config';
import { SnackbarService } from 'src/app/services/snackbar.service';
import { LocalServerData, VpnSavedDataService } from 'src/app/services/vpn-saved-data.service';

export interface EditVpnServerParams {
  server: LocalServerData;
  editName: boolean;
}

@Component({
  selector: 'app-edit-vpn-server-value',
  templateUrl: './edit-vpn-server-value.component.html',
  styleUrls: ['./edit-vpn-server-value.component.scss']
})
export class EditVpnServerValueComponent implements OnInit {
  @ViewChild('firstInput') firstInput: ElementRef;
  form: FormGroup;

  public static openDialog(dialog: MatDialog, params: EditVpnServerParams): MatDialogRef<EditVpnServerValueComponent, any> {
    const config = new MatDialogConfig();
    config.data = params;
    config.autoFocus = false;
    config.width = AppConfig.mediumModalWidth;

    return dialog.open(EditVpnServerValueComponent, config);
  }

  constructor(
    private dialogRef: MatDialogRef<EditVpnServerValueComponent>,
    @Inject(MAT_DIALOG_DATA) public data: EditVpnServerParams,
    private formBuilder: FormBuilder,
    private snackbarService: SnackbarService,
    private vpnSavedDataService: VpnSavedDataService,
  ) { }

  ngOnInit() {
    const savedValue = this.data.editName ? this.data.server.customName : this.data.server.personalNote;

    this.form = this.formBuilder.group({
      'value': [savedValue ? savedValue : '']
    });

    setTimeout(() => (this.firstInput.nativeElement as HTMLElement).focus());
  }

  process() {
    let updatedSavedServer = this.vpnSavedDataService.getSavedVersion(this.data.server.pk, true);
    updatedSavedServer = updatedSavedServer ? updatedSavedServer : this.data.server;

    const newValue = this.form.get('value').value;
    const currentValue = this.data.editName ? this.data.server.customName : this.data.server.personalNote;
    if (newValue === currentValue) {
      this.dialogRef.close();

      return;
    }

    if (this.data.editName) {
      updatedSavedServer.customName = newValue;
    } else {
      updatedSavedServer.personalNote = newValue;
    }

    this.vpnSavedDataService.updateServer(updatedSavedServer);
    this.snackbarService.showDone('vpn.server-options.edit-value.changes-made-confirmation');
    this.dialogRef.close(true);
  }
}
