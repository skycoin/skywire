import { Component, OnInit, Input, ViewChild, ElementRef, AfterViewInit, OnDestroy } from '@angular/core';
import { FormControl, FormGroup, Validators } from '@angular/forms';
import { Router } from '@angular/router';
import { MatDialog } from '@angular/material/dialog';
import { Subscription } from 'rxjs';

import { AuthService } from '../../../../services/auth.service';
import { SnackbarService } from '../../../../services/snackbar.service';
import { ButtonComponent } from '../../../layout/button/button.component';
import { OperationError } from '../../../../utils/operation-error';
import { processServiceError } from '../../../../utils/errors';

/**
 * Allows both to set the password for the first time and to change the existing password.
 */
@Component({
  selector: 'app-password',
  templateUrl: './password.component.html',
  styleUrls: ['./password.component.scss']
})
export class PasswordComponent implements OnInit, AfterViewInit, OnDestroy {
  @ViewChild('button') button: ButtonComponent;
  @ViewChild('firstInput') firstInput: ElementRef;

  /**
   * If true, the control is used for setting the password for the first time. If false,
   * it is used to change the current password.
   */
  @Input() forInitialConfig = false;

  form: FormGroup;

  private subscription: Subscription;
  private formSubscription: Subscription;

  constructor(
    private authService: AuthService,
    private router: Router,
    private snackbarService: SnackbarService,
    private dialog: MatDialog,
  ) { }

  ngOnInit() {
    // TODO: Password validation is not exactly the same as in the hypervisor code.
    this.form = new FormGroup({
      'oldPassword': new FormControl('', !this.forInitialConfig ? Validators.required : null),
      'newPassword': new FormControl('', Validators.compose([Validators.required, Validators.minLength(6), Validators.maxLength(64)])),
      'newPasswordConfirmation': new FormControl('', [Validators.required, this.validatePasswords.bind(this)]),
    });

    this.formSubscription = this.form.controls['newPassword'].valueChanges
      .subscribe(() => this.form.controls['newPasswordConfirmation'].updateValueAndValidity());
  }

  ngAfterViewInit() {
    if (this.forInitialConfig) {
      setTimeout(() => (this.firstInput.nativeElement as HTMLElement).focus());
    }
  }

  ngOnDestroy() {
    if (this.subscription) {
      this.subscription.unsubscribe();
    }

    this.formSubscription.unsubscribe();
  }

  changePassword() {
    if (this.form.valid && !this.button.disabled) {
      this.button.showLoading();

      if (!this.forInitialConfig) {
        this.subscription = this.authService.changePassword(this.form.get('oldPassword').value, this.form.get('newPassword').value)
          .subscribe(
            () => {
              this.router.navigate(['nodes']);
              this.snackbarService.showDone('settings.password.password-changed');
            }, (err: OperationError) => {
              this.button.showError();
              err = processServiceError(err);

              this.snackbarService.showError(err);
            },
          );
      } else {
        this.subscription = this.authService.initialConfig(this.form.get('newPassword').value).subscribe(
          () => {
            this.dialog.closeAll();
            this.snackbarService.showDone('settings.password.initial-config.done');
          }, err => {
            this.button.showError();
            err = processServiceError(err);

            // The errors are marked as temporary to close the snackbar when closing the modal window.
            this.snackbarService.showError(err, null, true);
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
