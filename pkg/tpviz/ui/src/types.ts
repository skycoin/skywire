// API response interfaces for the tpviz server

export interface Transport {
  edges: [string, string];
  type: string;
  isLocalOnly?: boolean;
}

export interface UptimeEntry {
  pk: string;
  on: boolean;
  version: string;
}

export interface ServiceInfo {
  services: string[];
  country: string;
}

export interface ServicesData {
  [pk: string]: ServiceInfo;
}

export interface IPGroupsData {
  enabled: boolean;
  total_groups: number;
  groups: { [pk: string]: number };
}

export interface LocalVisorTransport {
  id: string;
  type: string;
  remote_pk: string;
  sent_bytes: number;
  recv_bytes: number;
  sent_delta: number;
  recv_delta: number;
}

export interface LocalVisorRoute {
  route_id: number;
  transport_id: string;
  dst_pk: string;
  next_hop_pk: string;
  type: string;
}

export interface LocalVisorData {
  connected: boolean;
  pub_key: string;
  country: string;
  transports: LocalVisorTransport[];
  routes: LocalVisorRoute[];
  routes_count: number;
  total_sent_delta: number;
  total_recv_delta: number;
}

export interface DMSGServer {
  pk: string;
  address: string;
  country: string;
  available_sessions: number;
  server_type?: string;
  clients: string[];
}

export interface DMSGData {
  servers: DMSGServer[];
  entries: string[];
  entries_count: number;
}

export interface TPSStatusResponse {
  running: boolean;
  tps_pk: string;
}

export interface TPSTransportResponse {
  local_pk: string;
  remote_pk: string;
  type: string;
  id?: string;
}

export interface DMSGHealthResponse {
  status: string;
  error?: string;
  build_info?: string;
  message?: string;
}

export interface PingResponse {
  status: string;
  latency_ms?: number;
  latencies?: number[];
  min_ms?: number;
  max_ms?: number;
  avg_ms?: number;
  packet_loss?: number;
  error?: string;
  mode: string;
}

export interface AppState {
  name: string;
  status: number;          // 0=stopped, 1=running, 2=errored, 3=starting
  detailed_status: string;
  auto_start: boolean;
  port: number;
  args?: string[];
}

export interface HealthResponse {
  cache_max_age: number;
  auto_refresh: boolean;
  next_refresh_in: number;
}

export interface VisorInfo {
  id: string;
  connections: number;
  types: Set<string>;
  isLocal?: boolean;
  isRouteDestination?: boolean;
  isRouteHop?: boolean;
}

export interface VisorServiceEntry {
  services: string[];
  country: string;
}

export interface ConnectionInfo {
  pk: string;
  type: string;
}

export interface GroupBoundary {
  centroid: { x: number; y: number };
  radius: number;
  color: { background: string; border: string };
  label: string;
  flag: string;
  count: number;
  level: 'inner' | 'outer';
  groupKey: string;
  labelRect: { x: number; y: number; w: number; h: number } | null;
}

export interface GroupCentroid {
  x: number;
  y: number;
  radius: number;
  count: number;
}

export interface SatelliteOrbit {
  angle: number;
  lane: number;
  emoji: string;
  isDragged: boolean;
}

export interface PlacedCircle {
  x: number;
  y: number;
  radius: number;
}
