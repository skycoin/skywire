import { Component, ViewChild, ElementRef, OnInit } from '@angular/core';
import { MatDialogRef, MatDialogConfig, MatDialog } from '@angular/material/dialog';
import { FormGroup, FormBuilder } from '@angular/forms';

import { AppConfig } from 'src/app/app.config';

/**
 * Modal window for entering the password for connecting to a backend shown by the history of
 * SkysocksClientSettingsComponent. If the user presses the continue button, the modal window
 * is closed and the password is returned in the "afterClosed" envent, but with an hyphen "-"
 * added to the begining, to help avoiding problems while checking empty strings.
 */
@Component({
  selector: 'app-skysocks-client-password',
  templateUrl: './skysocks-client-password.component.html',
  styleUrls: ['./skysocks-client-password.component.scss']
})
export class SkysocksClientPasswordComponent implements OnInit {
  @ViewChild('firstInput', { static: false }) firstInput: ElementRef;

  form: FormGroup;

  /**
   * Opens the modal window. Please use this function instead of opening the window "by hand".
   */
  public static openDialog(dialog: MatDialog): MatDialogRef<SkysocksClientPasswordComponent, any> {
    const config = new MatDialogConfig();
    config.autoFocus = false;
    config.width = AppConfig.smallModalWidth;

    return dialog.open(SkysocksClientPasswordComponent, config);
  }

  constructor(
    private dialogRef: MatDialogRef<SkysocksClientPasswordComponent>,
    private formBuilder: FormBuilder,
  ) { }

  ngOnInit() {
    this.form = this.formBuilder.group({
      'password': [''],
    });

    setTimeout(() => (this.firstInput.nativeElement as HTMLElement).focus());
  }

  // Closes the modal window and returns the password.
  finish() {
    const password = this.form.get('password').value;
    this.dialogRef.close('-' + password);
  }
}
