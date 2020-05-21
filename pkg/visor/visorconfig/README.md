# V1

- `dmsg` (*[DmsgConfig](#DmsgConfig))
- `dmsgpty` (*[V1Dmsgpty](#V1Dmsgpty))
- `stcp` (*[STCPConfig](#STCPConfig))
- `transport` (*[V1Transport](#V1Transport))
- `routing` (*[V1Routing](#V1Routing))
- `uptime_tracker` (*[V1UptimeTracker](#V1UptimeTracker))
- `launcher` (*[V1Launcher](#V1Launcher))
- `hypervisors` ()
- `cli_addr` (string)
- `log_level` (string)
- `shutdown_timeout` (Duration)
- `restart_check_delay` (string)


# V1Launcher

- `discovery` (*[V1AppDisc](#V1AppDisc))
- `apps` ([][AppConfig](#AppConfig))
- `server_addr` (string)
- `bin_path` (string)
- `local_path` (string)


# V1Transport

- `discovery` (string)
- `log_store` (*[V1LogStore](#V1LogStore))
- `trusted_visors` ()


# V1AppDisc

- `update_interval` (Duration)
- `proxy_discovery_addr` (string)


# V1UptimeTracker

- `addr` (string)


# V1Routing

- `setup_nodes` ()
- `route_finder` (string)
- `route_finder_timeout` (Duration)


# V1LogStore

- `type` (string) - Type defines the log store type. Valid values: file, memory.
- `location` (string)


# V1Dmsgpty

- `port` (uint16)
- `authorization_file` (string)
- `cli_network` (string)
- `cli_address` (string)


# AppConfig

- `name` (string)
- `args` ([]string)
- `auto_start` (bool)
- `port` (Port)


# DmsgConfig

- `discovery` (string)
- `sessions_count` (int)


# STCPConfig

- `pk_table` ()
- `local_address` (string)
