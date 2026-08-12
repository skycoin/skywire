import { BrowserModule} from '@angular/platform-browser';
import { ErrorHandler, NgModule } from '@angular/core';
import { BrowserAnimationsModule } from '@angular/platform-browser/animations';
import { HttpBackend, provideHttpClient, withInterceptorsFromDi, withXhr } from '@angular/common/http';
import { SkywireHttpBackend } from './services/skywire-http-backend';
import { AppComponent } from './app.component';
import { AppRoutingModule } from './app-routing.module';
import { StartComponent } from './components/pages/start/start.component';
import { LoginComponent } from './components/pages/login/login.component';
import { NodeListComponent } from './components/pages/node-list/node-list.component';
import { NodeComponent } from './components/pages/node/node.component';
import { SharedModule } from './shared/shared.module';
import { AppTranslationModule } from './app-translation.module';
import { ErrorStateMatcher, ShowOnDirtyErrorStateMatcher, RippleGlobalOptions, MAT_RIPPLE_GLOBAL_OPTIONS } from '@angular/material/core';
import { MAT_DIALOG_DEFAULT_OPTIONS } from '@angular/material/dialog';
import { MatIconRegistry } from '@angular/material/icon';
import { MAT_SNACK_BAR_DEFAULT_OPTIONS } from '@angular/material/snack-bar';
import { TransportListComponent } from './components/pages/node/routing/transport-list/transport-list.component';
import { NodeAppsListComponent } from './components/pages/node/apps/node-apps-list/node-apps-list.component';
import { LogComponent } from './components/pages/node/apps/node-apps-list/log/log.component';
import { SettingsComponent } from './components/pages/settings/settings.component';
import { PasswordComponent } from './components/pages/settings/password/password.component';
import { ClipboardService } from './services/clipboard.service';
import { ClientErrorHandler } from './services/client-error-reporter';
import { ChartsComponent } from './components/pages/node/charts/charts.component';
import { RouteListComponent } from './components/pages/node/routing/route-list/route-list.component';
import { RoutingComponent } from './components/pages/node/routing/routing.component';
import { AppsComponent } from './components/pages/node/apps/apps.component';
import { CreateTransportComponent } from './components/pages/node/routing/transport-list/create-transport/create-transport.component';
import { BasicTerminalComponent } from './components/pages/node/actions/basic-terminal/basic-terminal.component';
import { RouteDetailsComponent } from './components/pages/node/routing/route-list/route-details/route-details.component';
import { RefreshRateComponent } from './components/pages/settings/refresh-rate/refresh-rate.component';
import { AllTransportsComponent } from './components/pages/node/routing/all-transports/all-transports.component';
import { NodeRewardsComponent } from './components/pages/node/rewards/node-rewards.component';
import { ServicesHealthComponent } from './components/pages/services-health/services-health.component';
import { DmsgSettingsComponent } from './components/pages/dmsg-settings/dmsg-settings.component';
import { AllRoutesComponent } from './components/pages/node/routing/all-routes/all-routes.component';
import { AllAppsComponent } from './components/pages/node/apps/all-apps/all-apps.component';
import { RouteReuseStrategy } from '@angular/router';
import { AppReuseStrategy } from './app.reuse-strategy';
import { TransportDetailsComponent } from './components/pages/node/routing/transport-list/transport-details/transport-details.component';
import { LogFilterComponent } from './components/pages/node/apps/node-apps-list/log/log-filter/log-filter.component';
import { InitialSetupComponent } from './components/pages/login/initial-setup/initial-setup.component';
import { ProxySettingsComponent } from './components/pages/node/actions/proxy-settings/proxy-settings.component';
import { SkynetComponent } from './components/pages/node/skynet/skynet.component';
import { NodeInfoContentComponent } from './components/pages/node/node-info/node-info-content/node-info-content.component';
import { ResourceMonitorComponent } from './components/pages/node/resource-monitor/resource-monitor.component';
import { NodeResourcesComponent } from './components/pages/node/node-resources/node-resources.component';
import { SkychatComponent } from './components/pages/node/skychat/skychat.component';
import { BandwidthComponent } from './components/pages/node/bandwidth/bandwidth.component';
import { UptimeComponent } from './components/pages/node/uptime/uptime.component';
import { TerminalComponent } from './components/pages/node/terminal/terminal.component';
import { WalletComponent } from './components/pages/node/wallet/wallet.component';
import { FullAppHostComponent } from './components/pages/full-app-host/full-app-host.component';
import { AppSettingsComponent } from './components/pages/node/apps/app-settings/app-settings.component';
import { WebProxyComponent } from './components/pages/node/web-proxy/web-proxy.component';
import { VpnComponent } from './components/pages/node/vpn/vpn.component';
import { SkysocksTabComponent } from './components/pages/node/skysocks-tab/skysocks.component';
import { SelectProxyServerComponent } from './components/pages/node/skysocks-tab/select-proxy-server/select-proxy-server.component';
import { LogsComponent } from './components/pages/node/logs/logs.component';
import { NetworkViewComponent } from './components/pages/network-view/network-view.component';
import { NetworkVisualizerComponent } from './components/pages/network-visualizer/network-visualizer.component';
import { MultiVisorResourcesComponent } from './components/pages/multi-visor-resources/multi-visor-resources.component';
import { MultiVisorUptimeComponent } from './components/pages/multi-visor-uptime/multi-visor-uptime.component';
import { NetworkTransportsComponent } from './components/pages/network-transports/network-transports.component';
import { NodeInfoComponent } from './components/pages/node/node-info/node-info.component';
import { AllLabelsComponent } from './components/pages/settings/all-labels/all-labels.component';
import { LabelListComponent } from './components/pages/settings/all-labels/label-list/label-list.component';
import { UpdaterConfigComponent } from './components/pages/settings/updater-config/updater-config.component';
import { RouterConfigComponent } from './components/pages/node/node-info/node-info-content/router-config/router-config.component';
import { RewardsAddressComponent } from './components/pages/node/node-info/node-info-content/rewards-address-config/rewards-address-config.component';
import { NodeLogsComponent } from './components/pages/node/actions/node-logs/node-logs.component';

const globalRippleConfig: RippleGlobalOptions = {
  disabled: true,
};

@NgModule({ declarations: [
        AppComponent,
        StartComponent,
        LoginComponent,
        NodeListComponent,
        NodeComponent,
        LogComponent,
        TransportListComponent,
        NodeAppsListComponent,
        SettingsComponent,
        PasswordComponent,
        ChartsComponent,
        RouteListComponent,
        RoutingComponent,
        AppsComponent,
        CreateTransportComponent,
        BasicTerminalComponent,
        RouteDetailsComponent,
        RefreshRateComponent,
        AllTransportsComponent,
        AllRoutesComponent,
        NodeRewardsComponent,
        ServicesHealthComponent,
        DmsgSettingsComponent,
        AllAppsComponent,
        TransportDetailsComponent,
        LogFilterComponent,
        InitialSetupComponent,
        ProxySettingsComponent,
        SkynetComponent,
        NodeInfoContentComponent,
        ResourceMonitorComponent,
        NodeResourcesComponent,
        SkychatComponent,
        BandwidthComponent,
        UptimeComponent,
        TerminalComponent,
        FullAppHostComponent,
        WalletComponent,
        AppSettingsComponent,
        WebProxyComponent,
        VpnComponent,
        SkysocksTabComponent,
        SelectProxyServerComponent,
        LogsComponent,
        NetworkViewComponent,
        NetworkVisualizerComponent,
        MultiVisorResourcesComponent,
        MultiVisorUptimeComponent,
        NetworkTransportsComponent,
        NodeInfoComponent,
        AllLabelsComponent,
        LabelListComponent,
        UpdaterConfigComponent,
        RouterConfigComponent,
        RewardsAddressComponent,
        NodeLogsComponent,
    ],
    bootstrap: [AppComponent], imports: [BrowserModule,
        BrowserAnimationsModule,
        AppRoutingModule,
        // Root translation setup (TranslateModule.forRoot: loader + service).
        AppTranslationModule,
        // SharedModule re-exports CommonModule, the forms modules, the common
        // Material list and the bare TranslateModule, plus the shared layout
        // components/pipe/directive — so the feature components declared here
        // (and future lazy feature modules) get them by importing it alone.
        SharedModule], providers: [
        ClipboardService,
        { provide: ErrorHandler, useClass: ClientErrorHandler },
        { provide: MAT_SNACK_BAR_DEFAULT_OPTIONS, useValue: { duration: 3000, verticalPosition: 'top' } },
        { provide: MAT_DIALOG_DEFAULT_OPTIONS, useValue: { width: '600px', hasBackdrop: true } },
        { provide: ErrorStateMatcher, useClass: ShowOnDirtyErrorStateMatcher },
        { provide: RouteReuseStrategy, useClass: AppReuseStrategy },
        { provide: MAT_RIPPLE_GLOBAL_OPTIONS, useValue: globalRippleConfig },
        provideHttpClient(withXhr(), withInterceptorsFromDi()),
        // Swap the bottom of the HttpClient pipeline so the SAME UI build talks to
        // either the native REST API (transparent pass-through) or the in-tab
        // wasm-visor gateway. Dormant unless window.__SKYWIRE_HV__ activates it.
        { provide: HttpBackend, useClass: SkywireHttpBackend },
    ] })
export class AppModule {
  // Angular Material 19+ defaults <mat-icon> to the
  // "Material Symbols Outlined" font, but we ship the older
  // "Material Icons" font locally (assets/fonts/material-icons/*).
  // Without this override every icon renders as a literal token like
  // "terminal" or "settings" because the symbols font isn't loaded —
  // the per-tab icon row in the per-visor view then overflows
  // horizontally as text. Wire the registry to our font on bootstrap.
  constructor(iconRegistry: MatIconRegistry) {
    iconRegistry.setDefaultFontSetClass('material-icons');
  }
}
