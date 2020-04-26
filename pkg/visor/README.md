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
- `apps` ([][AppConfig](#AppConfig))
- `trusted_visors` ()- `hypervisors` ([][HypervisorConfig](#HypervisorConfig))
- `apps_path` (string)
- `local_path` (string)
- `log_level` (string)
- `shutdown_timeout` (Duration)- `interfaces` (*[InterfaceConfig](#InterfaceConfig))
- `app_server_addr` (string)
- `restart_check_delay` (string)


# KeyPair

- `public_key` (PubKey)- `secret_key` (SecKey)

# TransportConfig

- `discovery` (string)
- `log_store` (*[LogStoreConfig](#LogStoreConfig))


# InterfaceConfig

- `rpc` (string)


# DmsgPtyConfig

- `port` (uint16)- `authorization_file` (string)
- `cli_network` (string)
- `cli_address` (string)


# RoutingConfig

- `setup_nodes` ()- `route_finder` (string)
- `route_finder_timeout` (Duration)

# UptimeTrackerConfig

- `addr` (string)


# AppConfig

- `app` (string)
- `auto_start` (bool)
- `port` (Port)- `args` ([]string)


# HypervisorConfig

- `public_key` (PubKey)

# LogStoreConfig

- `type` (LogStoreType)- `location` (string)


# Logger

- `` (FieldLogger)

# DmsgConfig

- `discovery` (string)
- `sessions_count` (int)


# STCPConfig

- `pk_table` ()- `local_address` (string)


# Mutex

- `state` (int32)
- `sema` (uint32)