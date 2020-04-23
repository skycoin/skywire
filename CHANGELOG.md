# Changelog
All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](http://keepachangelog.com/en/1.0.0/)
and this project adheres to [Semantic Versioning](http://semver.org/spec/v2.0.0.html).

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
