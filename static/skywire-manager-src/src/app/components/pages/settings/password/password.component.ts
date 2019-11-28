import { Component, OnInit, Input, ViewChild, ElementRef, AfterViewInit } from '@angular/core';
import { FormControl, FormGroup, Validators } from '@angular/forms';
import { Router } from '@angular/router';
import { AuthService } from '../../../../services/auth.service';
import { SnackbarService } from '../../../../services/snackbar.service';
import { MatDialog } from '@angular/material/dialog';
import { ButtonComponent } from '../../../layout/button/button.component';

@Component({
  selector: 'app-password',
  templateUrl: './password.component.html',
  styleUrls: ['./password.component.scss']
})
export class PasswordComponent implements OnInit, AfterViewInit {
  @ViewChild('button', { static: false }) button: ButtonComponent;
  @ViewChild('firstInput', { static: false }) firstInput: ElementRef;

  @Input() forInitialConfig = false;

  form: FormGroup;

  constructor(
    private authService: AuthService,
    private router: Router,
    private snackbarService: SnackbarService,
    private dialog: MatDialog,
  ) { }

  ngOnInit() {
    this.form = new FormGroup({
      'oldPassword': new FormControl('', !this.forInitialConfig ? Validators.required : null),
      'newPassword': new FormControl('', Validators.compose([Validators.required, Validators.minLength(6), Validators.maxLength(64)])),
      'newPasswordConfirmation': new FormControl('', [this.validatePasswords.bind(this)]),
    }, {
      validators: [this.validatePasswords.bind(this)],
    });
  }

  ngAfterViewInit() {
    if (this.forInitialConfig) {
      setTimeout(() => (this.firstInput.nativeElement as HTMLElement).focus());
    }
  }

  changePassword() {
    if (this.form.valid) {
      if (!this.forInitialConfig) {
        this.authService.changePassword(this.form.get('oldPassword').value, this.form.get('newPassword').value).subscribe(
          () => {
            this.router.navigate(['nodes']);
            this.snackbarService.showDone('settings.password.password-changed');
          }, (err) => {
            if (err.message) {
              this.snackbarService.showError(err.message);
            } else {
              this.snackbarService.showError('settings.password.error-changing');
            }
          },
        );
      } else {
        this.button.showLoading();

        this.authService.initialConfig(this.form.get('newPassword').value).subscribe(
          () => {
            this.dialog.closeAll();
            this.snackbarService.showDone('settings.password.initial-config.done');
          }, err => {
            this.button.showError();
            if (err.message) {
              this.snackbarService.showError(err.message);
            } else {
              this.snackbarService.showError('settings.password.initial-config.error');
            }
          },
        );
      }
    }
  }

  private validatePasswords() {
    if (this.form) {
      return this.form.get('newPassword').value !== this.form.get('newPasswordConfirmation').value
        ? { invalid: true } : null;
    } else {
      return null;
    }
  }
}
