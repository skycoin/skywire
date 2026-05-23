export interface DMSGServerInfo {
  pk: string;
  latency: number; // in nanoseconds
}

export class Node {
  label: string;
  localPk: string;
  isSymmeticNat?: boolean;
  publicIp?: string;
  ip: string;
  version: string;
  configVersion?: string;
  os?: string;
  arch?: string;
  apps: Application[];
  transports: Transport[];
  persistentTransports: PersistentTransport[];
  routesCount: number;
  minHops: number;
  routes?: Route[];
  online?: boolean;
  // Set on rows the hypervisor served from its summary cache because
  // the live RPC failed. Pair: offlineSince = when the live fetch
  // failed; lastSeenAt = when the cached snapshot was captured.
  // Both are ISO timestamps; both undefined on the never-seen path
  // (offline placeholder synthesized client-side from local storage).
  offlineSince?: string;
  lastSeenAt?: string;
  // True when the row's metadata came from the hypervisor's stale
  // cache rather than a live fetch. Convenience flag derived from
  // offlineSince — saves repeating the truthiness check in templates.
  isStale?: boolean;
  secondsOnline?: number;
  health?: HealthInfo;
  dmsgServerPk?: string;
  connectedDmsgServers?: string[]; // deprecated
  dmsgServers?: DMSGServerInfo[];  // DMSG servers with per-server latencies
  roundTripPing?: string;
  isHypervisor?: boolean;
  // hypervisors = configured (from skywire-config.json's hypervisors[]);
  // connectedHypervisors = currently connected sessions reported by
  // the visor. Both are 66-char hex PKs. Surfaced on the Info tab so
  // operators can see which hypervisors a visor reports to.
  hypervisors?: string[];
  connectedHypervisors?: string[];
  buildTag: string;
  skybianBuildVersion?: string;
  autoconnectTransports: boolean;
  isPublic?: boolean;
  rewardsAddress: string;
  countryCode?: string;
  regionName?: string;
  cityName?: string;
  latitude?: number;
  longitude?: number;
  // OS hostname of the host the visor runs on, populated from
  // os.Hostname() server-side. Surfaced on the Node Info tab and
  // used by storageService.getDefaultLabel() as the preferred
  // fallback when no explicit label is set (more operator-readable
  // than the visor's local IP or PK).
  hostname?: string;
}

export interface Application {
  name: string;
  autostart: boolean;
  port: number;
  status: number;
  detailedStatus: string;
  args: any[];
}

export interface Transport {
  id: string;
  localPk: string;
  remotePk: string;
  type: string;
  recv: number|null;
  sent: number|null;
  // Smoothed transport-level RTT in milliseconds. Populated from
  // the visor's TransportSummary.latency_ms field. 0/undefined =
  // no measurement yet (e.g., dmsg or freshly added transport).
  latencyMs?: number;

  // Calculated internally
  isPersistent?: boolean;
  notFound?: boolean;
}

export interface PersistentTransport {
  pk: string;
  type: string;
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
  servicesHealth?: string;
  uptimeTrackerHealth?: string;
  autoconnectHealth?: string;
  transportabilityHealth?: string;
}

export class ProxyDiscoveryEntry {
  address: string;
  pk: string;
  port: string;
  country?: string;
  region?: string;
  location?: string;
}
