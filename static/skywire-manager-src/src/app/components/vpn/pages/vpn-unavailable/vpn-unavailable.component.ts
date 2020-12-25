import { Component } from '@angular/core';
import { ActivatedRoute } from '@angular/router';
import { Subscription } from 'rxjs/internal/Subscription';

@Component({
  selector: 'app-vpn-unavailable',
  templateUrl: './vpn-unavailable.component.html',
  styleUrls: ['./vpn-unavailable.component.scss'],
})
export class VpnUnavailableComponent {
  private problem = null;

  private navigationsSubscription: Subscription;

  constructor(
    private route: ActivatedRoute,
  ) {
    // Get the query string.
    this.navigationsSubscription = this.route.queryParamMap.subscribe(queryParams => {
      this.problem = queryParams.get('problem');
      setTimeout(() => this.navigationsSubscription.unsubscribe());
    });
  }

  getTitle(): string {
    if (this.problem === 'pk') {
      return 'vpn.error-page.text-pk';
    } else if (this.problem === 'storage') {
      return 'vpn.error-page.text-storage';
    } else if (this.problem === 'pkChange') {
      return 'vpn.error-page.text-pk-change';
    }

    return 'vpn.error-page.text';
  }

  getInfo(): string {
    if (this.problem === 'pk') {
      return 'vpn.error-page.more-info-pk';
    } else if (this.problem === 'storage') {
      return 'vpn.error-page.more-info-storage';
    } else if (this.problem === 'pkChange') {
      return 'vpn.error-page.more-info-pk-change';
    }

    return 'vpn.error-page.more-info';
  }
}
