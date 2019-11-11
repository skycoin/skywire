import {Component, OnInit} from '@angular/core';
import {AuthService} from '../../../services/auth.service';
import {FormControl, FormGroup, Validators} from '@angular/forms';
import {Router} from '@angular/router';
import { SnackbarService } from '../../../services/snackbar.service';
import { MatDialog, MatDialogConfig } from '@angular/material/dialog';
import { InitialSetupComponent } from './initial-setup/initial-setup.component';

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

  onLoginSuccess() {
    this.router.navigate(['nodes']);
  }

  onLoginError() {
    this.loading = false;
    this.snackbarService.showError('login.incorrect-password');
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
    const config = new MatDialogConfig();
    config.autoFocus = false;
    config.width = '480px';
    this.dialog.open(InitialSetupComponent, config);
  }
}
