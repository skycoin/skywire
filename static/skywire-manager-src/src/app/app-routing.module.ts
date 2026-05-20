import { NgModule } from '@angular/core';
import { RouterModule, Routes } from '@angular/router';

import { StartComponent } from './components/pages/start/start.component';
import { LoginComponent } from './components/pages/login/login.component';
import { NodeListComponent } from './components/pages/node-list/node-list.component';
import { NodeComponent } from './components/pages/node/node.component';
import { AuthGuardService } from './services/auth-guard.service';
import { SettingsComponent } from './components/pages/settings/settings.component';
import { RoutingComponent } from './components/pages/node/routing/routing.component';
import { AppsComponent } from './components/pages/node/apps/apps.component';
import { NodeResourcesComponent } from './components/pages/node/node-resources/node-resources.component';
import { SkychatComponent } from './components/pages/node/skychat/skychat.component';
import { BandwidthComponent } from './components/pages/node/bandwidth/bandwidth.component';
import { UptimeComponent } from './components/pages/node/uptime/uptime.component';
import { TerminalComponent } from './components/pages/node/terminal/terminal.component';
import { WalletComponent } from './components/pages/node/wallet/wallet.component';
import { WebProxyComponent } from './components/pages/node/web-proxy/web-proxy.component';
import { VpnComponent } from './components/pages/node/vpn/vpn.component';
import { SkysocksTabComponent } from './components/pages/node/skysocks-tab/skysocks.component';
import { LogsComponent } from './components/pages/node/logs/logs.component';
import { AllTransportsComponent } from './components/pages/node/routing/all-transports/all-transports.component';
import { AllRoutesComponent } from './components/pages/node/routing/all-routes/all-routes.component';
import { NodeRewardsComponent } from './components/pages/node/rewards/node-rewards.component';
import { ServicesHealthComponent } from './components/pages/services-health/services-health.component';
import { NetworkViewComponent } from './components/pages/network-view/network-view.component';
import { MultiVisorResourcesComponent } from './components/pages/multi-visor-resources/multi-visor-resources.component';
import { MultiVisorUptimeComponent } from './components/pages/multi-visor-uptime/multi-visor-uptime.component';
import { NetworkTransportsComponent } from './components/pages/network-transports/network-transports.component';
import { DmsgSettingsComponent } from './components/pages/dmsg-settings/dmsg-settings.component';
import { AllAppsComponent } from './components/pages/node/apps/all-apps/all-apps.component';
import { NodeInfoComponent } from './components/pages/node/node-info/node-info.component';
import { SkynetComponent } from './components/pages/node/skynet/skynet.component';
import { AllLabelsComponent } from './components/pages/settings/all-labels/all-labels.component';
import { VpnServerListComponent } from './components/vpn/pages/vpn-server-list/vpn-server-list.component';
import { VpnStatusComponent } from './components/vpn/pages/vpn-status/vpn-status.component';
import { VpnErrorComponent } from './components/vpn/pages/vpn-error/vpn-error.component';
import { VpnSettingsComponent } from './components/vpn/pages/vpn-settings/vpn-settings.component';
import { VpnAuthGuardService } from './services/vpn-auth-guard.service';

const routes: Routes = [
  {
    path: '',
    component: StartComponent
  },
  {
    path: 'login',
    component: LoginComponent
  },
  {
    path: 'nodes',
    canActivate: [AuthGuardService],
    canActivateChild: [AuthGuardService],
    children: [
      {
        path: '',
        redirectTo: 'list/1',
        pathMatch: 'full'
      },
      {
        path: 'list',
        redirectTo: 'list/1',
        pathMatch: 'full'
      },
      {
        path: 'list/:page',
        component: NodeListComponent
      },
      {
        path: 'dmsg',
        redirectTo: 'rewards/1',
        pathMatch: 'full'
      },
      {
        path: 'dmsg/:page',
        redirectTo: 'rewards/1',
      },
      {
        path: 'rewards',
        redirectTo: 'rewards/1',
        pathMatch: 'full'
      },
      {
        path: 'rewards/:page',
        component: NodeListComponent
      },
      {
        path: 'services-health',
        component: ServicesHealthComponent
      },
      {
        path: 'network',
        component: NetworkViewComponent
      },
      {
        path: 'resources',
        component: MultiVisorResourcesComponent
      },
      {
        path: 'uptime',
        component: MultiVisorUptimeComponent
      },
      {
        path: 'transports',
        component: NetworkTransportsComponent
      },
      // /nodes/dmsg-settings was the old home-level DMSG page (local
      // visor only). DMSG is a per-visor tab now — bounce the legacy
      // URL to the home node list rather than 404.
      {
        path: 'dmsg-settings',
        redirectTo: 'list/1'
      },
      {
        path: ':key',
        component: NodeComponent,
        children: [
          {
            path: '',
            redirectTo: 'info',
            pathMatch: 'full'
          },
          {
            path: 'info',
            component: NodeInfoComponent
          },
          {
            path: 'routing',
            component: RoutingComponent
          },
          {
            path: 'apps',
            component: AppsComponent
          },
          {
            path: 'resources',
            component: NodeResourcesComponent
          },
          // Skychat / VPN / Skysocks moved into the Apps tab as
          // sub-tabs (the per-visor tab row was overflowing).
          // Redirect old top-level URLs to the Apps tab — the
          // operator picks the sub-tab from there.
          {
            path: 'chat',
            redirectTo: 'apps',
            pathMatch: 'full'
          },
          {
            path: 'bandwidth',
            component: BandwidthComponent
          },
          {
            path: 'uptime',
            component: UptimeComponent
          },
          {
            path: 'terminal',
            component: TerminalComponent
          },
          {
            path: 'wallet',
            component: WalletComponent
          },
          {
            path: 'web-proxy',
            component: WebProxyComponent
          },
          {
            path: 'vpn-tab',
            redirectTo: 'apps',
            pathMatch: 'full'
          },
          {
            path: 'skysocks',
            redirectTo: 'apps',
            pathMatch: 'full'
          },
          {
            path: 'logs',
            component: LogsComponent
          },
          // DMSG settings is now a collapsible section on the
          // Info tab (was a separate top-level tab). Redirect.
          {
            path: 'dmsg',
            redirectTo: 'info',
            pathMatch: 'full'
          },
          {
            path: 'transports',
            component: AllTransportsComponent
          },
          // Legacy paginated URLs — bounce to the new tab. Old
          // bookmarks (/.../transports/1, /.../routes/1) keep working.
          {
            path: 'transports/:page',
            redirectTo: 'transports'
          },
          {
            path: 'routes',
            redirectTo: 'routing',
            pathMatch: 'full'
          },
          {
            path: 'routes/:page',
            redirectTo: 'routing'
          },
          {
            path: 'rewards',
            component: NodeRewardsComponent
          },
          {
            path: 'skynet',
            component: SkynetComponent
          },
          // /dmsg is handled by the redirect-to-info entry above
          // (line ~187). Reachability tab was dropped from the
          // per-node tab row for the same reason DMSG was —
          // overflow on the tab row — but the URL is kept as a
          // redirect for back-compat with bookmarks.
          {
            path: 'reachability',
            redirectTo: 'info',
            pathMatch: 'full'
          },
          {
            path: 'apps-list/:showOfficialApps/:page',
            component: AllAppsComponent
          },
        ]
      },
    ],
  },
  {
    path: 'settings',
    canActivate: [AuthGuardService],
    canActivateChild: [AuthGuardService],
    children: [
      {
        path: '',
        component: SettingsComponent
      },
      {
        path: 'labels',
        redirectTo: 'labels/1',
        pathMatch: 'full'
      },
      {
        path: 'labels/:page',
        component: AllLabelsComponent
      },
    ],
  },
  {
    path: 'vpnlogin/:key',
    component: LoginComponent
  },
  {
    path: 'vpn',
    canActivate: [VpnAuthGuardService],
    canActivateChild: [VpnAuthGuardService],
    children: [
      {
        path: 'unavailable',
        component: VpnErrorComponent
      },
      {
        path: ':key',
        children: [
          {
            path: 'status',
            component: VpnStatusComponent
          },
          {
            path: 'servers',
            redirectTo: 'servers/public/1',
            pathMatch: 'full'
          },
          {
            path: 'servers/:type/:page',
            component: VpnServerListComponent
          },
          {
            path: 'settings',
            component: VpnSettingsComponent
          },
          {
            path: '**',
            redirectTo: 'status'
          }
        ]
      },
      {
        path: '**',
        redirectTo: '/vpn/unavailable?problem=pk'
      }
    ],
  },
  {
    path: '**',
    redirectTo: ''
  },
];

@NgModule({
  imports: [RouterModule.forRoot(routes, {
    useHash: true
})],
  exports: [RouterModule],
})
export class AppRoutingModule {
}


