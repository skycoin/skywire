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
import { AllTransportsComponent } from './components/pages/node/routing/all-transports/all-transports.component';
import { AllRoutesComponent } from './components/pages/node/routing/all-routes/all-routes.component';
import { AllAppsComponent } from './components/pages/node/apps/all-apps/all-apps.component';
import { NodeInfoComponent } from './components/pages/node/node-info/node-info.component';
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
        redirectTo: 'dmsg/1',
        pathMatch: 'full'
      },
      {
        path: 'dmsg/:page',
        component: NodeListComponent
      },
      {
        path: ':key',
        component: NodeComponent,
        children: [
          {
            path: '',
            redirectTo: 'routing',
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
            path: 'transports',
            redirectTo: 'transports/1',
            pathMatch: 'full'
          },
          {
            path: 'transports/:page',
            component: AllTransportsComponent
          },
          {
            path: 'routes',
            redirectTo: 'routes/1',
            pathMatch: 'full'
          },
          {
            path: 'routes/:page',
            component: AllRoutesComponent
          },
          {
            path: 'apps-list',
            redirectTo: 'apps-list/1',
            pathMatch: 'full'
          },
          {
            path: 'apps-list/:page',
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
    useHash: true,
  })],
  exports: [RouterModule],
})
export class AppRoutingModule {
}


