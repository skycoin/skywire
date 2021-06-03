export class Node {
  label: string;
  localPk: string;
  ip: string;
  version: string;
  apps: Application[];
  transports: Transport[];
  routesCount: number;
  minHops: number;
  routes?: Route[];
  online?: boolean;
  secondsOnline?: number;
  health?: HealthInfo;
  dmsgServerPk?: string;
  roundTripPing?: string;
  isHypervisor?: boolean;
}

export interface Application {
  name: string;
  autostart: boolean;
  port: number;
  status: number;
  args: any[];
}

export interface Transport {
  isUp: boolean;
  id: string;
  localPk: string;
  remotePk: string;
  type: string;
  recv: number|null;
  sent: number|null;
}

export interface Route {
  key: number;
  rule: string;
  ruleSummary?: RouteRuleSummary;
  appFields?: RouteAppRuleSumary;
  forwardFields?: RouteForwardRuleSumary;
  intermediaryForwardFields?: RouteForwardRuleSumary;
}

export interface RouteRuleSummary {
  keepAlive: number;
  ruleType: number;
  keyRouteId: number;
}

interface RouteAppRuleSumary {
  routeDescriptor: RouteDescriptor;
}

interface RouteForwardRuleSumary {
  nextRid: number;
  nextTid: string;
  routeDescriptor?: RouteDescriptor;
}

interface RouteDescriptor {
  dstPk: string;
  srcPk: string;
  dstPort: number;
  srcPort: number;
}

export interface HealthInfo {
  status: number;
  transportDiscovery: number;
  routeFinder: number;
  setupNode: number;
  uptimeTracker: number;
  addressResolver: number;
}

export class ProxyDiscoveryEntry {
  address: string;
  pk: string;
  port: string;
  country?: string;
  region?: string;
  location?: string;
}
