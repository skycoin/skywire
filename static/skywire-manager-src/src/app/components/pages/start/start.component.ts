import { Component, OnInit, OnDestroy } from '@angular/core';
import { Router } from '@angular/router';
import { Subscription } from 'rxjs';

import { AuthService, AuthStates } from '../../../services/auth.service';

/**
 * Initial utility page for redirecting the user to the real appropriate initial page.
 * It redirects to the login page if the user is unauthorized and to the visor list in
 * all other cases.
 */
@Component({
  selector: 'app-start',
  templateUrl: './start.component.html',
  styleUrls: ['./start.component.scss']
})
export class StartComponent implements OnInit, OnDestroy {
  private verificationSubscription: Subscription;

  constructor(
    private authService: AuthService,
    private router: Router,
  ) { }

  ngOnInit() {
    // Check if the user is unauthorized.
    this.verificationSubscription = this.authService.checkLogin().subscribe(response => {
      if (response !== AuthStates.NotLogged) {
        this.router.navigate(['nodes'], { replaceUrl: true });
      } else {
        this.router.navigate(['login'], { replaceUrl: true });
      }
    }, () => {
      // In case of error, go to the visor list. While trying to get the list, additional
      // comprobations will be performed in that page.
      this.router.navigate(['nodes'], { replaceUrl: true });
    });
  }

  ngOnDestroy() {
    this.verificationSubscription.unsubscribe();
  }
}
