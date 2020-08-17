// Most classes are based on the responses returned by the API, but
// sometimes with some extra fields which are calculated internally in the app.

export class Node {
  tcp_addr: string;
  ip: string;
  port: string;
  local_pk: string;
  node_version: string;
  app_protocol_version: string;
  apps: Application[];
  transports: Transport[];
  routes_count: number;
  routes?: Route[];
  label?: string;
  online?: boolean;
  seconds_online?: number;
  health?: HealthInfo;
  dmsgServerPk?: string;
  roundTripPing?: string;
}

export interface Application {
  name: string;
  autostart: boolean;
  port: number;
  status: number;
  args?: any[];
}

export interface Transport {
  id: string;
  local_pk: string;
  remote_pk: string;
  type: string;
  log?: TransportLog;
  is_up: boolean;
}

export interface TransportLog {
  recv: number|null;
  sent: number|null;
}

export interface Route {
  key: number;
  rule: string;
}

export interface HealthInfo {
  status?: number;
  transport_discovery?: number;
  route_finder?: number;
  setup_node?: number;
}

export class ProxyDiscoveryEntry {
  address: string;
  pk: string;
  port: string;
  country?: string;
  region?: string;
  location?: string;
}
