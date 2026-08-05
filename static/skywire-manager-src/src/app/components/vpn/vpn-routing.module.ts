import { NgModule } from '@angular/core';
import { RouterModule, Routes } from '@angular/router';

import { VpnAuthGuardService } from '../../services/vpn-auth-guard.service';
import { VpnErrorComponent } from './pages/vpn-error/vpn-error.component';
import { VpnStatusComponent } from './pages/vpn-status/vpn-status.component';
import { VpnServerListComponent } from './pages/vpn-server-list/vpn-server-list.component';
import { VpnSettingsComponent } from './pages/vpn-settings/vpn-settings.component';

/**
 * Child route table for the lazy-loaded VPN feature (mounted at /vpn by
 * app-routing.module.ts via loadChildren). Mirrors the former eager /vpn
 * subtree exactly; the parent /vpn route keeps canActivate, and this module
 * keeps canActivateChild so navigation between VPN pages stays guarded.
 */
const routes: Routes = [
  {
    path: '',
    canActivateChild: [VpnAuthGuardService],
    children: [
      {
        path: 'unavailable',
        component: VpnErrorComponent,
      },
      {
        path: ':key',
        children: [
          {
            path: 'status',
            component: VpnStatusComponent,
          },
          {
            path: 'servers',
            redirectTo: 'servers/public/1',
            pathMatch: 'full',
          },
          {
            path: 'servers/:type/:page',
            component: VpnServerListComponent,
          },
          {
            path: 'settings',
            component: VpnSettingsComponent,
          },
          {
            path: '**',
            redirectTo: 'status',
          },
        ],
      },
      {
        path: '**',
        redirectTo: '/vpn/unavailable?problem=pk',
      },
    ],
  },
];

@NgModule({
  imports: [RouterModule.forChild(routes)],
  exports: [RouterModule],
})
export class VpnRoutingModule { }
