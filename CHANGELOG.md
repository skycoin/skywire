# Changelog
All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](http://keepachangelog.com/en/1.0.0/)
and this project adheres to [Semantic Versioning](http://semver.org/spec/v2.0.0.html).

## 1.3.0

-   Fix delete reward file [#1441](https://github.com/skycoin/skywire/pull/1441)
-   Show reward address on autoconfig [#1441](https://github.com/skycoin/skywire/pull/1441)
-   disable public autoconnect logic for `config gen -b` [#1440](https://github.com/skycoin/skywire/pull/1440)
-   Add changelog generation script [#1439](https://github.com/skycoin/skywire/pull/1439)
-   Fix GetRewardAddress API  [#1438](https://github.com/skycoin/skywire/pull/1438)
-   fix release issues  [#1432](https://github.com/skycoin/skywire/pull/1432) [#1434](https://github.com/skycoin/skywire/pull/1434) [#1433](https://github.com/skycoin/skywire/pull/1433)
-   built tag for non-systray skywire-visor  [#1429](https://github.com/skycoin/skywire/pull/1429)
-   visor test subcommand  [#1428](https://github.com/skycoin/skywire/pull/1428)
-   Update to Angular 15  [#1426](https://github.com/skycoin/skywire/pull/1426)
-   Hot fix on DNS  [#1425](https://github.com/skycoin/skywire/pull/1425)
-   update dmsg@develop  [#1423](https://github.com/skycoin/skywire/pull/1423)
-   Selected DMSG Server  [#1422](https://github.com/skycoin/skywire/pull/1422)
-   Integrated Autoconfig  [#1417](https://github.com/skycoin/skywire/pull/1417)
-   Update Angular to v14.2.11  [#1416](https://github.com/skycoin/skywire/pull/1416)
-   skywire-cli log collecting command  [#1414](https://github.com/skycoin/skywire/pull/1414)
-   App/Services showing ports subcommand `skywire-cli visor ports`  [#1412](https://github.com/skycoin/skywire/pull/1412)
-   skywire app example  [#1409](https://github.com/skycoin/skywire/pull/1409)
-   `skywire-cli doc` command & cli documentation update  [#1408](https://github.com/skycoin/skywire/pull/1408)
-   fixing skywire-cli reward freezing issue  [#1407](https://github.com/skycoin/skywire/pull/1407)
-   Improve readme documentation  [#1406](https://github.com/skycoin/skywire/pull/1406)
-   Add cli command visor ping and test  [#1405](https://github.com/skycoin/skywire/pull/1405)
-   build ui  [#1403](https://github.com/skycoin/skywire/pull/1403)
-   fix `make format check` errors  [#1401](https://github.com/skycoin/skywire/pull/1401)
-   Fix control visor apps from hv  [#1399](https://github.com/skycoin/skywire/pull/1399)
-   Bug fixes for the UI  [#1398](https://github.com/skycoin/skywire/pull/1398)
-   run as systray flag `--systray`  [#1396](https://github.com/skycoin/skywire/pull/1396)
-   fix panic and datarace  [#1394](https://github.com/skycoin/skywire/pull/1394)
-   Printing new IP after connecting to VPN in CLI  [#1393](https://github.com/skycoin/skywire/pull/1393)
-   Add display node ip field to the main config  [#1392](https://github.com/skycoin/skywire/pull/1392)
-   re-implement setting reward address  [#1391](https://github.com/skycoin/skywire/pull/1391)
-   skywire-cli terminal user interface improvements  [#1390](https://github.com/skycoin/skywire/pull/1390)
-   improve `skywire-cli vpn` subcommand  [#1389](https://github.com/skycoin/skywire/pull/1389)
-   Fix transport logging  [#1386](https://github.com/skycoin/skywire/pull/1386)
-   fix cli config priv flags  [#1384](https://github.com/skycoin/skywire/pull/1384)
-   Add param customCommand for PtyUI.Handler  [#1383](https://github.com/skycoin/skywire/pull/1383)
-   add Info field to Service struct  [#1382](https://github.com/skycoin/skywire/pull/1382)
-   Add DNS to TUN, in VPN-Client  [#1381](https://github.com/skycoin/skywire/pull/1381)
-   Improve systray VPN button initialization  [#1380](https://github.com/skycoin/skywire/pull/1380)
-   fix privacyjson  [#1379](https://github.com/skycoin/skywire/pull/1379)
-   Update transport file logging  [#1376](https://github.com/skycoin/skywire/pull/1376)
-   Update LocalIPs field in model Service  [#1375](https://github.com/skycoin/skywire/pull/1375)
-   expose dmsghttp server  [#1374](https://github.com/skycoin/skywire/pull/1374)
-   Fix negative waitgroup  [#1372](https://github.com/skycoin/skywire/pull/1372)
-   `skywire-cli config priv` subcommand  [#1369](https://github.com/skycoin/skywire/pull/1369)
-   fix absence of git in makefile  [#1368](https://github.com/skycoin/skywire/pull/1368)
-   Fix rpc error in cli for json  [#1367](https://github.com/skycoin/skywire/pull/1367)
-   Fix StartVPNCient logic  [#1366](https://github.com/skycoin/skywire/pull/1366)

## 1.2.0

### Added
- `skywire-cil visor hv` subcommand [#1390](https://github.com/skycoin/skywire/pull/1390)
- info field to Service struct [#1382](https://github.com/skycoin/skywire/pull/1382)
- `skywire-cli` subcommand `arg` under `visor app` [#1356](https://github.com/skycoin/skywire/pull/1356)
- `log_store` field to `transport` in config [#1386](https://github.com/skycoin/skywire/pull/1386)
- `type`, `location`, `rotation_interval`, field to `log_store` inside `transport` in config [#1374](https://github.com/skycoin/skywire/pull/1374)
- transport file logging to CSV [#1374](https://github.com/skycoin/skywire/pull/1374)
- `skywire-cli config priv` & `skywire-cli visor priv` subcommands and rpc [#1369](https://github.com/skycoin/skywire/issues/1369)
- dmsghttp server [#1364](https://github.com/skycoin/skywire/issues/1364)
- `display_node_ip` field to `launcher` in config [#1392](https://github.com/skycoin/skywire/pull/1392)

### Changed
- moved `skywire-cli visor` subcommands into `skywire-cil visor hv` [#1390](https://github.com/skycoin/skywire/pull/1390)
- use flags for `skywire-cli visor route` & `skywire-cli visor tp` [#1390](https://github.com/skycoin/skywire/pull/1390)
- moved `skywire-cli` subcommand `autoconnect` from `visor app` to `visor app arg` [#1356](https://github.com/skycoin/skywire/pull/1356)

### Fixed
- negative waitgroup  [#1372](https://github.com/skycoin/skywire/pull/1372)
- absence of git in makefile  [#1368](https://github.com/skycoin/skywire/pull/1368)
- rpc error in cli for json [#1367](https://github.com/skycoin/skywire/pull/1367)
- StartVPNCient logic [#1366](https://github.com/skycoin/skywire/pull/1366)

## 1.1.0

### Added

- `skywire-cli` global flag `--json` [#1346](https://github.com/skycoin/skywire/pull/1346)
- service discovery query filtering for `skywire-cli vpn list`	[#1337](https://github.com/skycoin/skywire/pull/1337)
- `skywire-cli vpn` subcommands	[#1317](https://github.com/skycoin/skywire/pull/1317)
- separate systray application which uses `skywire-cli vpn` subcommands	[#1317](https://github.com/skycoin/skywire/pull/1317)
- port of the autopeering system from skybian to the skywire source code.  [#1309](https://github.com/skycoin/skywire/pull/1309)
- `-l --hvip` and `-m --autopeer` flags for `skywire-visor` ; connect to a hypervisor by ip address.  [#1309](https://github.com/skycoin/skywire/pull/1309)
- `skywire-cli visor pk -w` flag ; http endpoint for visor public key [#1309](https://github.com/skycoin/skywire/pull/1309)
- `-y --autoconn` and `-z --ispublic` flags for `skywire-cli config gen` [#1319](https://github.com/skycoin/skywire/pull/1319)
- error packet to routes to propagate route errors [#1181](https://github.com/skycoin/skywire/issues/1181)
- `skywire-cli chvpk` subcommand to list remote hypervisor(s) a visor is currently connected to [#1306](https://github.com/skycoin/skywire/issues/1306)
- pong packet to send as a response to ping to calculate latency [#1261](https://github.com/skycoin/skywire/issues/1261)
- store UI settings per hypervisor key [#1329](https://github.com/skycoin/skywire/pull/1329)

### Changed

- `skywire-cli visor route add-rule` subcommands [#1346](https://github.com/skycoin/skywire/pull/1346)
- Autopeer on env `AUTOPEER=1`
- improve UI reaction while system is busy
- hide password options in UI if authentication is disabled
- fix freezing hypervisor UI on hypervisor disconnection [#1321](https://github.com/skycoin/skywire/issues/1321)
- fix route setup hooks to check if transport to remote is established [#1297](https://github.com/skycoin/skywire/issues/1297)
- rename network probe packet to ping [#1261](https://github.com/skycoin/skywire/issues/1261)
- added Value/Scan method to SWAddr for using in DB directly
- added new fields (ID, CreatedAT) to Service type for using in DB directly
- fixed entrypoint.sh for Dockerfile [#1336](https://github.com/skycoin/skywire/pull/1336)

### Removed

- `skywire-cli visor tp add` flag `--public` [#1346](https://github.com/skycoin/skywire/pull/1346)
- remove updater settings from UI

### Fixed
- UI update button [#1349](https://github.com/skycoin/skywire/pull/1349)

## 1.0.0

### Added

- `skywire-cli hv` subcommands for opening the various UIs or printing links to them (HVUI, VPNUI, DMSGPTYUI) [#1270](https://github.com/skycoin/skywire/pull/1270)
- added `add-rhv` and `disable-rhv` flags to `skywire-visor` for adding remote hypervisor PK and disable remote hypervisor PK(s) on config file [#1113](https://github.com/skycoin/skywire/pull/1113)
- shorthand flags for commands [#1151](https://github.com/skycoin/skywire/pull/1151)
- blue & white color scheme with coloredcobra [#1151](https://github.com/skycoin/skywire/pull/1151)
- ascii art text modal of program name to help menus [#1151](https://github.com/skycoin/skywire/pull/1151)
- `--all` flag to skywire-cli & visor to show extra flags [#1151](https://github.com/skycoin/skywire/pull/1151)
- `skywire-cli config gen -n --stdout` write config to stdout [#1151](https://github.com/skycoin/skywire/pull/1151)
- `skywire-cli config gen   -w, --hide` dont print the config to the terminal [#1151](https://github.com/skycoin/skywire/pull/1151)
- `skywire-cli config gen --print` parse test ; read config from file & print [#1151](https://github.com/skycoin/skywire/pull/1151)
- `skywire-cli config gen   -a, --url` services conf (default "conf.skywire.skycoin.com") [#1151](https://github.com/skycoin/skywire/pull/1151)
- fetch service from endpoint [#1151](https://github.com/skycoin/skywire/pull/1151)
- `skywire-cli visor app` app settings command [#1132](https://github.com/skycoin/skywire/pull/1132)
- `skywire-cli visor route` view and set rules command [#1132](https://github.com/skycoin/skywire/pull/1132)
- `skywire-cli visor tp` view and set transports command [#1132](https://github.com/skycoin/skywire/pull/1132)
- `skywire-cli visor vpn` vpn interface command [#1132](https://github.com/skycoin/skywire/pull/1132)
- root permissions detection
- error on different version config / visor
- display update command on config version error
- support for piping config generated by skywire-cli to skywire-visor via stdin [#1147](https://github.com/skycoin/skywire/pull/1147)
- support for detecting skywire version when `go run`
- `run-vpnsrv` makefile directive [#1147](https://github.com/skycoin/skywire/pull/1147)
- `run-source-test` makefile directive [#1147](https://github.com/skycoin/skywire/pull/1147)
- `run-vpnsrv-test` makefile directive [#1147](https://github.com/skycoin/skywire/pull/1147)
- `run-source-dmsghttp` makefile directive [#1147](https://github.com/skycoin/skywire/pull/1147)
- `run-source-dmsghttp-test` makefile directive [#1147](https://github.com/skycoin/skywire/pull/1147)
- `run-vpnsrv-dmsghttp` makefile directive [#1147](https://github.com/skycoin/skywire/pull/1147)
- `run-vpnsrv -dmsghttp-test` makefile directive [#1147](https://github.com/skycoin/skywire/pull/1147)
- `install-system-linux` and `install-system-linux-systray` makefile directives [#1180](https://github.com/skycoin/skywire/pull/1180)
- `skywire-cli dmsgpty list` to view of connected remote visor to hypervisor [#1250](https://github.com/skycoin/skywire/pull/1250)
- `skywire-cli dmsgpty start <pk>` to connect through dmsgpty to remote visor [#1250](https://github.com/skycoin/skywire/pull/1250)
- `make win-installer-latest` to create installer for latest version of released, not pre-release.
- `trace` log level is added
- `--log-level` flag to generate and update config by `skywire-cli`

### Changed
- remove dsmghttp migration to skywire-visor starting
- only support current version of config
- config version reflects current visor version (`1.0.0`)
- refine and restructure help commands user interface
- shorthand flags for commands
- group skywire-cli visor subcommands
- hide excess flags
- make help text fit within default 80x24 terminal
- rename `skywire-cli config gen -r --replace` flag to `-r --regen`
- remove config path from V1 struct
- remove all instance of the visor writing to the config file except via api
- remove path to dmsghttp-config.json from config
- revise versioning
- move to skyenv
- remove transports cache from visor initialization and check them before make route
- `run-source` makefile directive write config to stdout & read config from stdin
- fixed skywire-visor uses skywire-config.json (default config name) without needing to specify
- `make win-installer` need new argument `CUSTOM_VERSION` to get make installer for this version, use for pre-releases
- changed the log levels of most of the logs making info level clutter free

### Removed

- inbuilt updater ; instead use packages and the system package manger for installation and updates [#1251](https://github.com/skycoin/skywire/pull/1251)

## 0.6.0


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

### Changed

- detecting OS in runtime removed
- skybian flag `-s` removed from `skywire-cli config gen`
- migrate updating logic to debian package model

## 0.5.0

### Added

- added persistent_transports field to the config and UI
- added stun_servers field to the config
- added is_public field to root section
- added public_autoconnect field to transport section
- added transport_setup_nodes field to transport section
- added MinHops field to V1Routing section of config
- added `skywire-cli config` subcommand
- added connection_duration field to `/api/visor/{pk}/apps/vpn-client/connections`

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
