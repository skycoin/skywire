import { Component, OnInit } from '@angular/core';
import { FormControl, FormGroup, Validators } from '@angular/forms';
import { Router } from '@angular/router';
import { MatDialog, MatDialogConfig } from '@angular/material/dialog';

import { AuthService } from '../../../services/auth.service';
import { SnackbarService } from '../../../services/snackbar.service';
import { InitialSetupComponent } from './initial-setup/initial-setup.component';

/**
 * Login page.
 */
@Component({
  selector: 'app-login',
  templateUrl: './login.component.html',
  styleUrls: ['./login.component.scss']
})
export class LoginComponent implements OnInit {
  form: FormGroup;
  loading = false;

  constructor(
    private authService: AuthService,
    private router: Router,
    private snackbarService: SnackbarService,
    private dialog: MatDialog,
  ) { }

  ngOnInit() {
    this.form = new FormGroup({
      'password': new FormControl('', Validators.required),
    });
  }

  login() {
    if (!this.form.valid) {
      return;
    }

    this.loading = true;
    this.authService.login(this.form.get('password').value).subscribe(
      () => this.onLoginSuccess(),
      () => this.onLoginError()
    );
  }

  configure() {
    InitialSetupComponent.openDialog(this.dialog);
  }

  private onLoginSuccess() {
    this.router.navigate(['nodes']);
  }

  private onLoginError() {
    this.loading = false;
    this.snackbarService.showError('login.incorrect-password');
  }
}
