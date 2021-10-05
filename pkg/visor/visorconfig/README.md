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
- `local_path` (string)
- `stun_servers` ([]string)
- `shutdown_timeout` (Duration)
- `restart_check_delay` (Duration)
- `is_public` (bool)
- `persistent_transports` ([][PersistentTransports](#PersistentTransports))
- `hypervisor` (*[Config](#Config))


# V1Launcher

- `service_discovery` (string)
- `apps` ([][AppConfig](#AppConfig))
- `server_addr` (string)
- `bin_path` (string)


# V1Transport

- `discovery` (string)
- `address_resolver` (string)
- `public_autoconnect` (bool)
- `transport_setup_nodes` ()


# V1Routing

- `setup_nodes` ()
- `route_finder` (string)
- `route_finder_timeout` (Duration)
- `min_hops` (uint16)


# V1UptimeTracker

- `addr` (string)


# V1Dmsgpty

- `port` (uint16)
- `cli_network` (string)
- `cli_address` (string)


# PersistentTransports

- `pk` (PubKey)
- `type` (Type)


# DmsgConfig

- `discovery` (string)
- `sessions_count` (int)


# STCPConfig

- `pk_table` ()
- `local_address` (string)


# Config

- `db_path` (string)
- `enable_auth` (bool)
- `cookies` ([CookieConfig](#CookieConfig))
- `dmsg_port` (uint16)
- `http_addr` (string)
- `enable_tls` (bool)
- `tls_cert_file` (string)
- `tls_key_file` (string)


# CookieConfig

- `hash_key` (Key)
- `block_key` (Key)
- `expires_duration` (Duration)
- `path` (string)
- `domain` (string)


# AppConfig

- `name` (string)
- `args` ([]string)
- `auto_start` (bool)
- `port` (Port)
