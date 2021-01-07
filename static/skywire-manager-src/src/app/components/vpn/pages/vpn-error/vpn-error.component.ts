import { Component } from '@angular/core';
import { ActivatedRoute } from '@angular/router';
import { Subscription } from 'rxjs/internal/Subscription';

import { VpnAuthGuardService } from 'src/app/services/vpn-auth-guard.service';
import { VpnClientService } from 'src/app/services/vpn-client.service';

/**
 * Errors VpnErrorComponent can show.
 */
enum KnownProblems {
  UnableToConnectWithTheVpnClientApp = 'unavailable',
  NoLocalVisorPkProvided = 'pk',
  InvalidStorageState = 'storage',
  LocalVisorPkChangedDuringUsage = 'pkChange',
}

/**
 * Page for showing an important error which made the VPN client stop working.
 * For indicating the problem, use the "problem" var in the querystring. You can check the valid
 * values in the KnownProblems enum. If no value is provided,
 * KnownProblems.UnableToConnectWithTheVpnClientApp is used.
 */
@Component({
  selector: 'app-vpn-error',
  templateUrl: './vpn-error.component.html',
  styleUrls: ['./vpn-error.component.scss'],
})
export class VpnErrorComponent {
  private problem = null;

  private navigationsSubscription: Subscription;

  constructor(
    private route: ActivatedRoute,
    private vpnAuthGuardService: VpnAuthGuardService,
    private vpnClientService: VpnClientService,
  ) {
    // Get the query string.
    this.navigationsSubscription = this.route.queryParamMap.subscribe(queryParams => {
      this.problem = queryParams.get('problem');
      if (!this.problem) {
        this.problem = KnownProblems.UnableToConnectWithTheVpnClientApp;
      }

      // Make all navigations redirect to this page.
      this.vpnAuthGuardService.lastError = this.problem;
      // Stop requesting data.
      this.vpnClientService.stopContinuallyUpdatingData();

      setTimeout(() => this.navigationsSubscription.unsubscribe());
    });
  }

  // Returns the translatable var for the top text.
  getTitle(): string {
    if (this.problem === KnownProblems.NoLocalVisorPkProvided) {
      return 'vpn.error-page.text-pk';
    } else if (this.problem === KnownProblems.InvalidStorageState) {
      return 'vpn.error-page.text-storage';
    } else if (this.problem === KnownProblems.LocalVisorPkChangedDuringUsage) {
      return 'vpn.error-page.text-pk-change';
    }

    return 'vpn.error-page.text';
  }

  // Returns the translatable var for the lower text.
  getInfo(): string {
    if (this.problem === KnownProblems.NoLocalVisorPkProvided) {
      return 'vpn.error-page.more-info-pk';
    } else if (this.problem === KnownProblems.InvalidStorageState) {
      return 'vpn.error-page.more-info-storage';
    } else if (this.problem === KnownProblems.LocalVisorPkChangedDuringUsage) {
      return 'vpn.error-page.more-info-pk-change';
    }

    return 'vpn.error-page.more-info';
  }
}
