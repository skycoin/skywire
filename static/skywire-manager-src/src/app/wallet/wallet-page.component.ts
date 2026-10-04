import { ChangeDetectionStrategy, Component } from '@angular/core';
import { Router } from '@angular/router';

/**
 * The page the wallet lives on, at #/wallet: a bar back to the dashboard over
 * the wallet's own shell. The wallet keeps its wallets in this origin's
 * localStorage, so it is the same wallet whichever visor is selected.
 */
@Component({
  selector: 'app-wallet-page',
  templateUrl: './wallet-page.component.html',
  styleUrls: ['../components/pages/full-app-host/full-app-host.component.scss'],
  standalone: false,
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class WalletPageComponent {
  constructor(private router: Router) {}

  goHome(): void {
    this.router.navigate(['/nodes', 'list', '1']);
  }
}

