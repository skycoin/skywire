import { ChangeDetectionStrategy, Component, Input, OnChanges } from '@angular/core';

import { Node } from '../../../../../app.datatypes';

/** One line in the alerts list: a translation key, its parameters and how serious it is. */
export interface OverviewAlert {
  level: 'warn' | 'info';
  text: string;
  params?: { [key: string]: string | number };
}

/** Transport counts for one type, from the visor's point of view. */
interface TransportType {
  type: string;
  count: number;
  incoming: number;
  outgoing: number;
}

// Apps report 1 when running, 3 while connecting, 0 when stopped and 2 when they failed.
const appRunning = 1;
const appConnecting = 3;
const appFailed = 2;

// How long after start a health check that has not passed counts as a problem.
const healthGraceSeconds = 600;

/**
 * Whether a visor version and the version that wrote its config are the same
 * release. A build adds its commit to the version the config records
 * (v1.3.103-122-gabc against v1.3.103-122-gabc-abc1234), which is not a mismatch.
 */
export function sameRelease(version: string, configVersion: string): boolean {
  const release = (v: string) => /^v?(\d+\.\d+\.\d+)/.exec(v)?.[1];
  const a = release(version);
  const b = release(configVersion);

  return a && b ? a === b : version.startsWith(configVersion);
}

/**
 * Builds the alerts for a visor from the summary the hypervisor already sends,
 * so the overview needs no request of its own.
 */
export function overviewAlerts(node: Node): OverviewAlert[] {
  const alerts: OverviewAlert[] = [];
  const health = node.health || {};
  // The visor reports each check as "healthy" or "connecting" until it passes
  // (internalHealthInfo.value in pkg/visor/api.go), so only time tells a slow
  // check from a failing one.
  const settled = (node.secondsOnline || 0) >= healthGraceSeconds;
  const checks: [string | undefined, string][] = [
    [health.uptimeTrackerHealth, 'node.overview.alert-uptime-tracker'],
    [health.autoconnectHealth, 'node.overview.alert-autoconnect'],
    [health.transportabilityHealth, 'node.overview.alert-transportability'],
  ];
  checks.forEach(([state, text]) => {
    if (state && state !== 'healthy') {
      alerts.push({ level: settled ? 'warn' : 'info', text: text });
    }
  });
  (node.apps || []).forEach(app => {
    if (app.status === appFailed) {
      alerts.push({ level: 'warn', text: 'node.overview.alert-app-failed', params: { app: app.name } });
    } else if (app.autostart && app.status !== appRunning && app.status !== appConnecting) {
      alerts.push({ level: 'warn', text: 'node.overview.alert-app-stopped', params: { app: app.name } });
    }
  });
  if (node.transports && node.transports.length === 0) {
    alerts.push({ level: 'warn', text: 'node.overview.alert-no-transports' });
  }
  if (node.dmsgServers && node.dmsgServers.length === 0) {
    alerts.push({ level: 'warn', text: 'node.overview.alert-no-dmsg' });
  }
  if (!node.rewardsAddress) {
    alerts.push({ level: 'info', text: 'node.overview.alert-no-reward' });
  } else if (node.rewardEligible === false) {
    alerts.push({
      level: 'warn', text: 'node.overview.alert-reward-ineligible',
      params: { reason: node.rewardIneligibleReason || '-' },
    });
  }
  if (node.version && node.configVersion && !sameRelease(node.version, node.configVersion)) {
    alerts.push({ level: 'info', text: 'node.overview.alert-config-version', params: { config: node.configVersion } });
  }
  if (node.isSymmeticNat) {
    alerts.push({ level: 'info', text: 'node.overview.alert-symmetric-nat' });
  }

  return alerts;
}

/**
 * The top of a visor's Info tab: its health, transports, routes, apps and
 * rewards in one row of cards, with anything that needs attention listed
 * below them. Everything comes from the visor summary the page already loads.
 */
@Component({
  selector: 'app-visor-overview',
  templateUrl: './visor-overview.component.html',
  styleUrls: ['./visor-overview.component.scss'],
  standalone: false,
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class VisorOverviewComponent implements OnChanges {
  @Input() node!: Node;

  alerts: OverviewAlert[] = [];
  transportTypes: TransportType[] = [];
  incoming = 0;
  outgoing = 0;
  // Older visors send no direction for their transports.
  knowsDirection = false;
  appsRunning = 0;
  appsAutostart = 0;
  appsFailed = 0;

  ngOnChanges(): void {
    if (!this.node) {
      return;
    }
    this.alerts = overviewAlerts(this.node);

    const byType: { [type: string]: TransportType } = {};
    this.incoming = 0;
    this.outgoing = 0;
    (this.node.transports || []).forEach(t => {
      const type = (t.type || '?').toUpperCase();
      const entry = byType[type] || (byType[type] = { type: type, count: 0, incoming: 0, outgoing: 0 });
      entry.count++;
      if (t.initiator) {
        entry.outgoing++;
        this.outgoing++;
      } else {
        entry.incoming++;
        this.incoming++;
      }
    });
    this.transportTypes = Object.values(byType).sort((a, b) => b.count - a.count);
    this.knowsDirection = (this.node.transports || []).some(t => t.initiator !== undefined);

    const apps = this.node.apps || [];
    this.appsRunning = apps.filter(a => a.status === appRunning).length;
    this.appsAutostart = apps.filter(a => a.autostart).length;
    this.appsFailed = apps.filter(a => a.status === appFailed).length;
  }

  /** The css class for a health value: healthy, unhealthy or still connecting. */
  healthClass(state: string | undefined): string {
    return state === 'healthy' ? 'ok' : state === 'unhealthy' ? 'bad' : 'wait';
  }
}
