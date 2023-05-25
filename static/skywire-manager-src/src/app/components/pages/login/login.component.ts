import { Component, OnInit, OnDestroy } from '@angular/core';
import { UntypedFormControl, UntypedFormGroup, Validators } from '@angular/forms';
import { ActivatedRoute, Router } from '@angular/router';
import { MatDialog } from '@angular/material/dialog';
import { Subscription } from 'rxjs';
import { HttpErrorResponse } from '@angular/common/http';

import { AuthService, AuthStates } from '../../../services/auth.service';
import { SnackbarService } from '../../../services/snackbar.service';
import { InitialSetupComponent } from './initial-setup/initial-setup.component';
import { OperationError } from '../../../utils/operation-error';
import { processServiceError } from '../../../utils/errors';
import { AppComponent } from 'src/app/app.component';
import { MultipleNodeDataService } from 'src/app/services/multiple-node-data.service';
import { PageBaseComponent } from 'src/app/utils/page-base';

/**
 * Login page.
 */
@Component({
  selector: 'app-login',
  templateUrl: './login.component.html',
  styleUrls: ['./login.component.scss']
})
export class LoginComponent extends PageBaseComponent implements OnInit, OnDestroy {
  form: UntypedFormGroup;
  loading = false;
  isForVpn = false;
  vpnKey = '';

  private verificationSubscription: Subscription;
  private loginSubscription: Subscription;
  private routeSubscription: Subscription;

  constructor(
    private authService: AuthService,
    private router: Router,
    private snackbarService: SnackbarService,
    private dialog: MatDialog,
    private route: ActivatedRoute,
    private multipleNodeDataService: MultipleNodeDataService,
  ) {
    super();
  }

  ngOnInit() {
    // Stop multiple requests that will fail for auth.
    this.multipleNodeDataService.stopRequestingData();

    this.routeSubscription = this.route.paramMap.subscribe(params => {
      this.vpnKey = params.get('key');

      this.isForVpn = window.location.href.indexOf('vpnlogin') !== -1;

      // Check if the user is already logged.
      this.verificationSubscription = this.authService.checkLogin().subscribe(response => {
        if (response !== AuthStates.NotLogged) {
          // Inform about the redirect a frame before it is done, to avoid problems.
          AppComponent.currentInstance.processLoginDone();
          setTimeout(() => {
            const destination = !this.isForVpn ? ['nodes'] : ['vpn', this.vpnKey, 'status'];
            this.router.navigate(destination, { replaceUrl: true });
          }, 5);
        }
      });
    });

    this.form = new UntypedFormGroup({
      password: new UntypedFormControl('', Validators.required),
    });

    return super.ngOnInit();
  }

  ngOnDestroy() {
    if (this.loginSubscription) {
      this.loginSubscription.unsubscribe();
    }

    this.verificationSubscription.unsubscribe();
    this.routeSubscription.unsubscribe();
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
    // Inform about the redirect a frame before it is done, to avoid problems.
    AppComponent.currentInstance.processLoginDone();
    setTimeout(() => {
      const destination = !this.isForVpn ? ['nodes'] : ['vpn', this.vpnKey, 'status'];
      this.router.navigate(destination, { replaceUrl: true });
    });
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
