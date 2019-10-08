export interface Node {
  tcp_addr: string;
  local_pk: string;
  apps: Application[];
  transports: Transport[];
  routes_count: number;
}

export interface Application {
  name: string;
  autostart: boolean;
  port: number;
  status: number;
}

export interface Transport {
  id: string;
  local_pk: string;
  remote_pk: string;
  type: string;
  log?: TransportLog;
}

export interface TransportLog {
  recv: number|null;
  sent: number|null;
}

export interface Route {
  key: number;
  rule: string;
}



// old


export interface NodeFeedback {
  key: string;
  port: number;
  failed: boolean;
  unread: number;
}

export interface ClientConnection {
  label: string;
  nodeKey: string;
  appKey: string;
  count: number;
}

export interface LogMessage {
  time: number;
  msg: string;
}

export interface AutoStartConfig {
  sshs: boolean;
  sshc: boolean;
  sshc_conf_nodeKey: string;
  sshc_conf_appKey: string;
  sshc_conf_discovery: string;
  sockss: boolean;
  socksc: boolean;
  socksc_conf_nodeKey: string;
  socksc_conf_appKey: string;
  socksc_conf_discovery: string;
}

export interface Keypair {
  nodeKey: string;
  appKey: string;
}

export interface SearchResult {
  result: SearchResultItem[];
  seq: number;
  count: number;
}

export interface SearchResultItem {
  node_key: string;
  app_key: string;
  location: string;
  version: string;
  node_version: string[];
}
