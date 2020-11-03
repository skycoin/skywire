import { Component, OnInit, OnDestroy } from '@angular/core';
import { FormControl, FormGroup, Validators } from '@angular/forms';
import { Router } from '@angular/router';
import { MatDialog } from '@angular/material/dialog';
import { Subscription } from 'rxjs';
import { HttpErrorResponse } from '@angular/common/http';

import { AuthService, AuthStates } from '../../../services/auth.service';
import { SnackbarService } from '../../../services/snackbar.service';
import { InitialSetupComponent } from './initial-setup/initial-setup.component';
import { OperationError } from '../../../utils/operation-error';
import { processServiceError } from '../../../utils/errors';

/**
 * Login page.
 */
@Component({
  selector: 'app-login',
  templateUrl: './login.component.html',
  styleUrls: ['./login.component.scss']
})
export class LoginComponent implements OnInit, OnDestroy {
  form: FormGroup;
  loading = false;

  private verificationSubscription: Subscription;
  private loginSubscription: Subscription;

  constructor(
    private authService: AuthService,
    private router: Router,
    private snackbarService: SnackbarService,
    private dialog: MatDialog,
  ) { }

  ngOnInit() {
    // Check if the user is already logged.
    this.verificationSubscription = this.authService.checkLogin().subscribe(response => {
      if (response !== AuthStates.NotLogged) {
        this.router.navigate(['nodes'], { replaceUrl: true });
      }
    });

    this.form = new FormGroup({
      'password': new FormControl('', Validators.required),
    });
  }

  ngOnDestroy() {
    if (this.loginSubscription) {
      this.loginSubscription.unsubscribe();
    }

    this.verificationSubscription.unsubscribe();
  }

  login() {
    if (!this.form.valid || this.loading) {
      return;
    }

    this.loading = true;
    this.loginSubscription = this.authService.login(this.form.get('password').value).subscribe(
      () => this.onLoginSuccess(),
      err => this.onLoginError(err)
    );
  }

  configure() {
    InitialSetupComponent.openDialog(this.dialog);
  }

  private onLoginSuccess() {
    this.router.navigate(['nodes'], { replaceUrl: true });
  }

  private onLoginError(err: OperationError) {
    err = processServiceError(err);
    this.loading = false;

    if (err.originalError && (err.originalError as HttpErrorResponse).status === 401) {
      this.snackbarService.showError('login.incorrect-password');
    } else {
      this.snackbarService.showError(err.translatableErrorMsg);
    }
  }
}
