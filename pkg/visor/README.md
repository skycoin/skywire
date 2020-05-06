# Config

- `-` (*string)
- `log` (*[Logger](#Logger))
- `flushMu` ([Mutex](#Mutex))
- `version` (string)
- `key_pair` (*[KeyPair](#KeyPair))
- `dmsg` (*[DmsgConfig](#DmsgConfig))
- `dmsg_pty` (*[DmsgPtyConfig](#DmsgPtyConfig))
- `stcp` (*[STCPConfig](#STCPConfig))
- `transport` (*[TransportConfig](#TransportConfig))
- `routing` (*[RoutingConfig](#RoutingConfig))
- `uptime_tracker` (*[UptimeTrackerConfig](#UptimeTrackerConfig))
- `app_discovery` (*[AppDiscConfig](#AppDiscConfig))
- `apps` ([][AppConfig](#AppConfig))
- `app_server_addr` (string)
- `apps_path` (string)
- `local_path` (string)
- `trusted_visors` ([][PubKey](#PubKey))
- `hypervisors` ([][HypervisorConfig](#HypervisorConfig))
- `interfaces` (*[InterfaceConfig](#InterfaceConfig))
- `log_level` (string)
- `shutdown_timeout` ([Duration](#Duration))
- `restart_check_delay` (string)


# UptimeTrackerConfig

- `addr` (string)


# KeyPair

- `public_key` ([PubKey](#PubKey))
- `secret_key` ([SecKey](#SecKey))


# HypervisorConfig

- `public_key` ([PubKey](#PubKey))


# AppDiscConfig

- `update_interval` ([Duration](#Duration))
- `proxy_discovery_addr` (string)


# RoutingConfig

- `setup_nodes` ([][PubKey](#PubKey))
- `route_finder` (string)
- `route_finder_timeout` ([Duration](#Duration))


# DmsgPtyConfig

- `port` ([uint16](#uint16))
- `authorization_file` (string)
- `cli_network` (string)
- `cli_address` (string)


# TransportConfig

- `discovery` (string)
- `log_store` (*[LogStoreConfig](#LogStoreConfig))


# InterfaceConfig

- `rpc` (string)


# AppConfig

- `app` (string)
- `auto_start` (bool)
- `port` ([Port](#Port))
- `args` ([]string)


# LogStoreConfig

- `type` ([LogStoreType](#LogStoreType))
- `location` (string)


# STCPConfig

- `pk_table` (map[[PubKey](#PubKey)]string)
- `local_address` (string)


# Mutex

- `state` (int32)
- `sema` ([uint32](#uint32))


# DmsgConfig

- `discovery` (string)
- `sessions_count` (int)


# Logger

- `` ([FieldLogger](#FieldLogger))
