import { Component } from '@angular/core';
import { ActivatedRoute } from '@angular/router';
import { Subscription } from 'rxjs/internal/Subscription';

@Component({
  selector: 'app-vpn-unavailable',
  templateUrl: './vpn-unavailable.component.html',
  styleUrls: ['./vpn-unavailable.component.scss'],
})
export class VpnUnavailableComponent {
  problem = null;

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
}
