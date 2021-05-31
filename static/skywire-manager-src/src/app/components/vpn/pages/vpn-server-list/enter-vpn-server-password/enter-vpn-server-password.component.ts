import { Component, ViewChild, ElementRef, OnInit, Inject } from '@angular/core';
import { FormBuilder, FormGroup, Validators } from '@angular/forms';
import { MatDialogRef, MatDialog, MatDialogConfig, MAT_DIALOG_DATA } from '@angular/material/dialog';

import { AppConfig } from 'src/app/app.config';

/**
 * Modal window for requesting the password of a VPN server. After finishing, the modal
 * window is closed and the password is returned in the "afterClosed" envent, with a "-"
 * added to the start.
 */
@Component({
  selector: 'app-enter-vpn-server-password',
  templateUrl: './enter-vpn-server-password.component.html',
  styleUrls: ['./enter-vpn-server-password.component.scss']
})
export class EnterVpnServerPasswordComponent implements OnInit {
  @ViewChild('firstInput') firstInput: ElementRef;
  form: FormGroup;

  /**
   * Opens the modal window. Please use this function instead of opening the window "by hand".
   * @param allowEmpty If true, the user will be able to use an empty password.
   */
  public static openDialog(dialog: MatDialog, allowEmpty: boolean): MatDialogRef<EnterVpnServerPasswordComponent, any> {
    const config = new MatDialogConfig();
    config.data = allowEmpty;
    config.autoFocus = false;
    config.width = AppConfig.mediumModalWidth;

    return dialog.open(EnterVpnServerPasswordComponent, config);
  }

  constructor(
    private dialogRef: MatDialogRef<EnterVpnServerPasswordComponent>,
    @Inject(MAT_DIALOG_DATA) public data: boolean,
    private formBuilder: FormBuilder,
  ) { }

  ngOnInit() {
    this.form = this.formBuilder.group({
      'password': ['', this.data ? undefined : Validators.required]
    });

    setTimeout(() => (this.firstInput.nativeElement as HTMLElement).focus());
  }

  // Closes the modal window and returns the password.
  process() {
    this.dialogRef.close('-' + this.form.get('password').value);
  }
}
