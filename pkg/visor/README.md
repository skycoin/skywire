# Config

- `version` (string)
- `key_pair` (*[KeyPair](#KeyPair))
- `dmsg` (*[DmsgConfig](#DmsgConfig))
- `dmsg_pty` (*[DmsgPtyConfig](#DmsgPtyConfig))
- `stcp` (*[STCPConfig](#STCPConfig))
- `transport` (*[TransportConfig](#TransportConfig))
- `routing` (*[RoutingConfig](#RoutingConfig))
- `uptime_tracker` (*[UptimeTrackerConfig](#UptimeTrackerConfig))
- `apps` ([][AppConfig](#AppConfig))
- `trusted_visors` ([][PubKey](#PubKey))
- `hypervisors` ([][HypervisorConfig](#HypervisorConfig))
- `apps_path` (string)
- `local_path` (string)
- `log_level` (string)
- `shutdown_timeout` ([Duration](#Duration))
- `interfaces` (*[InterfaceConfig](#InterfaceConfig))
- `app_server_addr` (string)
- `restart_check_delay` (string)


# KeyPair

- `public_key` ([PubKey](#PubKey))
- `secret_key` ([SecKey](#SecKey))


# RoutingConfig

- `setup_nodes` ([][PubKey](#PubKey))
- `route_finder` (string)
- `route_finder_timeout` ([Duration](#Duration))


# TransportConfig

- `discovery` (string)
- `log_store` (*[LogStoreConfig](#LogStoreConfig))


# AppConfig

- `app` (string)
- `auto_start` (bool)
- `port` ([Port](#Port))
- `args` ([]string)


# DmsgPtyConfig

- `port` (uint16)
- `authorization_file` (string)
- `cli_network` (string)
- `cli_address` (string)


# HypervisorConfig

- `public_key` ([PubKey](#PubKey))
- `address` (string)


# InterfaceConfig

- `rpc` (string)


# UptimeTrackerConfig

- `addr` (string)


# LogStoreConfig

- `type` ([LogStoreType](#LogStoreType))
- `location` (string)


# DmsgConfig

- `discovery` (string)
- `sessions_count` (int)


# STCPConfig

- `pk_table` (map[[PubKey](#PubKey)]string)
- `local_address` (string)
