# V1

- `` (*[Common](#Common))
- `mu` ([RWMutex](#RWMutex))
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
- `public_trusted_visor` (bool)
- `hypervisor` (*[Config](#Config))


# V1Routing

- `setup_nodes` ()
- `route_finder` (string)
- `route_finder_timeout` (Duration)


# V1Dmsgpty

- `port` (uint16)
- `authorization_file` (string)
- `cli_network` (string)
- `cli_address` (string)


# V1Transport

- `discovery` (string)
- `address_resolver` (string)
- `log_store` (*[V1LogStore](#V1LogStore))
- `trusted_visors` ()


# V1LogStore

- `type` (string) - Type defines the log store type. Valid values: file, memory.
- `location` (string)


# V1Launcher

- `discovery` (*[V1AppDisc](#V1AppDisc))
- `apps` ([][AppConfig](#AppConfig))
- `server_addr` (string)
- `bin_path` (string)
- `local_path` (string)


# V1UptimeTracker

- `addr` (string)


# V1AppDisc

- `update_interval` (Duration)
- `proxy_discovery_addr` (string)


# Common

- `path` (string)
- `log` (*[MasterLogger](#MasterLogger))
- `version` (string)
- `sk` (SecKey)
- `pk` (PubKey)


# RWMutex

- `w` ([Mutex](#Mutex))
- `writerSem` (uint32)
- `readerSem` (uint32)
- `readerCount` (int32)
- `readerWait` (int32)


# Mutex

- `state` (int32)
- `sema` (uint32)


# STCPConfig

- `pk_table` ()
- `local_address` (string)


# AppConfig

- `name` (string)
- `args` ([]string)
- `auto_start` (bool)
- `port` (Port)


# DmsgConfig

- `discovery` (string)
- `sessions_count` (int)


# Config

- `-` (PubKey)
- `-` (SecKey)
- `db_path` (string)
- `enable_auth` (bool)
- `cookies` ([CookieConfig](#CookieConfig))
- `-` (string)
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
- `-` (bool)


# MasterLogger

- `` (*[Logger](#Logger))


# Logger

- `` (FieldLogger)
