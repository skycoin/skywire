import { NgModule } from '@angular/core';

import { SharedModule } from '../../shared/shared.module';
import { VpnRoutingModule } from './vpn-routing.module';

import { VpnErrorComponent } from './pages/vpn-error/vpn-error.component';
import { VpnServerListComponent } from './pages/vpn-server-list/vpn-server-list.component';
import { AddVpnServerComponent } from './pages/vpn-server-list/add-vpn-server/add-vpn-server.component';
import { EditVpnServerValueComponent } from './pages/vpn-server-list/edit-vpn-server-value/edit-vpn-server-value.component';
import { VpnStatusComponent } from './pages/vpn-status/vpn-status.component';
import { VpnSettingsComponent } from './pages/vpn-settings/vpn-settings.component';
import { VpnServerNameComponent } from './layout/vpn-server-name/vpn-server-name.component';
import { VpnDnsConfigComponent } from './layout/vpn-dns-config/vpn-dns-config.component';

/**
 * Lazy-loaded feature module for the VPN client UI (the /vpn route tree).
 *
 * First lazy conversion under the GUI embedding standardization
 * (docs/design/gui-embedding-standardization.md, step 4): the eight VPN
 * components move out of the monolithic AppModule into their own module that
 * imports SharedModule for the common layout components / Material / router /
 * translate. RouterConfigComponent (opened via a dialog from vpn-settings)
 * stays declared in AppModule and resolves cross-module through its own compiled
 * scope, so it needs no move.
 */
@NgModule({
  declarations: [
    VpnErrorComponent,
    VpnServerListComponent,
    AddVpnServerComponent,
    EditVpnServerValueComponent,
    VpnStatusComponent,
    VpnSettingsComponent,
    VpnServerNameComponent,
    VpnDnsConfigComponent,
  ],
  imports: [
    SharedModule,
    VpnRoutingModule,
  ],
})
export class VpnModule { }
