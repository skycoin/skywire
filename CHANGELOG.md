# Changelog
All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](http://keepachangelog.com/en/1.0.0/)
and this project adheres to [Semantic Versioning](http://semver.org/spec/v2.0.0.html).

## 0.6.0

### Changed

- detecting OS in runtime removed
- skybian flag `-s` removed from `skywire-cli config gen`
- config updated to `v1.1.1`


### Added

- added `update` and `summary` as subcommand to `skywire-cli visor`
- added multiple new flag to update configuration in `skywire-cli config update`
- added shell autocompletion command to `skywire-cli` and `skywire-visor`
- added `dsmgHTTPStruct` in visorconfig pkg to usable other repos, such as `skybian`
- added `dmsghttp-config.json` which contains the `dmsg-urls` of services and info of `dmsg-servers` for both prod and test
- added `servers` filed to `dmsg` in config
- added `-d,--dmsghttp` flag to `skywire-cli config gen`
- added `dmsgdirect` client to connect to services over dmsg
- added `-f` flag to skywire-visor to configure a visor to expose hypervisor UI with default values at runtime
- added `--public-rpc` falg to `skywire-cli config gen`
- added `--vpn-server-enable` falg to `skywire-cli config gen`
- added `--os` flag to `skywire-cli config gen`
- added `--disable-apps` flag to `skywire-cli config gen`
- added `--disable-auth` and `--enable-auth` flags to `skywire-cli config gen`
- added `--best-protocol` flag to `skywire-cli config gen`
- added `skywire-cli visor vpn-ui` and `skywire-cli visor vpn-url` commands
- added dsmghttp migration to skywire-visor starting
- added network monitor PKs to skyenv
## 0.5.0

### Changed

- config updated to `v1.1.0`
- removed public_trusted_visor field from root section
- removed trusted_visors field from transport section
- removed authorization_file field from dmsgpty section
- changed default urls to newer shortned ones
- changed proxy_discovery_addr field to service_discovery
- updated UI
- removed `--public` flag from `skywire-cli visor add-tp` command
- removed `skywire-cli visor gen-config` and `skywire-cli visor update-config` subcommands.
- replaced stcp field to skywire-tcp in config and comments
- replaced local_address field to listening_address in config
- replaced port field to dmsg_port in config
- updated visor health status checks, no longer querying multiple external services endpoints.


### Added

- added persistent_transports field to the config and UI
- added stun_servers field to the config
- added is_public field to root section
- added public_autoconnect field to transport section
- added transport_setup_nodes field to transport section
- added MinHops field to V1Routing section of config
- added `skywire-cli config` subcommand
- added connection_duration field to `/api/visor/{pk}/apps/vpn-client/connections`

## 0.2.1 - 2020.04.07

### Changed

- reverted port changes for `skysocks-client`

## 0.2.0 - 2020.04.02

### Added 

- added `--retain-keys` flag to `skywire-cli visor gen-config` command
- added `--secret-key` flag to `skywire-cli visor gen-config` command
- added hypervisorUI frontend
- added default values for visor if certain fields of config are empty

### Fixed

- fixed deployment route finder HTTP request
- fixed /user endpoint not working when auth is disabled

### Changed

- changed port of hypervisorUI and applications
- replaced unix sockets for app to visor communication to tcp sockets
- reverted asynchronous sending of router packets

## 0.1.0 - 2020.04.02

First release of Skywire Mainnet.
