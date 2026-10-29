# Config Bootstrapper

| Status   | Implemented                                                              |
|----------|--------------------------------------------------------------------------|
| Revision | 2026-10-27, skywire f37a464a4 |
| Code     | `pkg/deployment/conf`, `pkg/services/confbs`, `deployment`               |

## Abstract

The Config Bootstrapper serves the deployment's service configuration. It
returns the list of deployment services with their dmsg addresses, the dmsg
servers to bootstrap from, and the key lists that visors trust. It publishes the
same document as a signed CXO feed.

## Overview

A visor that has no configuration for the deployment asks the bootstrapper for
the services and the dmsg servers it needs to reach them. A running visor
subscribes to the CXO feed and so follows changes to the document without a new
release. The document served is `deployment.Services`, which is also embedded in
the binary as `deployment.Prod`.

## Addressing

The bootstrapper is served over dmsg HTTP at `dmsg://<conf-pk>:80`. The dmsg
port is the service setting `dmsg_port`, `dmsg.DefaultDmsgHTTPPort` when 0. The
CXO feed is served on dmsg port 78 (`skyenv.DmsgConfCXOPort`).

## Data Formats

### Services document

`deployment.Services`, the body of `GET /` and the content of the CXO leaf. Every
field is optional in the encoding.

| JSON key                  | Type              | Meaning                                    |
|---------------------------|-------------------|--------------------------------------------|
| `dmsg_discovery`, `transport_discovery`, `address_resolver`, `route_finder`, `service_discovery` | string | service addresses |
| `dmsg_discovery_dmsg`, `transport_discovery_dmsg`, `address_resolver_dmsg`, `route_finder_dmsg`, `service_discovery_dmsg` | string | `dmsg://<pk>:80` addresses of the services |
| `reward_system`, `reward_system_dmsg` | string | reward system address                 |
| `conf_dmsg`               | string            | address of the bootstrapper                |
| `route_setup_nodes`       | []string          | public keys of route setup nodes           |
| `transport_setup`         | []string          | public keys of transport setup nodes       |
| `survey_whitelist`        | []string          | public keys allowed to read surveys        |
| `stun_servers`            | []string          | STUN servers                               |
| `dns_server`, `geoip`     | string            | DNS and geoip addresses                    |
| `dmsg_servers`            | []object          | `deployment.DmsgServerEntry`: `static` (server key), `server.address`, optional `server.address_ws` |
| `wss_domain_suffix`, `wss_domain_aliases`, `browse_origin_suffix`, `wasm_module_source` | | settings for browser visors |

### dmsghttp document

`httputil.DMSGHTTPConf`, the body of `GET /dmsghttp`.

| JSON key              | Type     | Meaning                                       |
|-----------------------|----------|-----------------------------------------------|
| `dmsg_servers`        | []object | `static` and `server.address` of each server (`httputil.DMSGServersConf`) |
| `dmsg_discovery`      | string   | `dmsg://` address of dmsg discovery           |
| `transport_discovery` | string   | `dmsg://` address of Transport Discovery      |
| `address_resolver`    | string   | `dmsg://` address of the Address Resolver     |
| `route_finder`        | string   | `dmsg://` address of the Route Finder         |
| `service_discovery`   | string   | `dmsg://` address of Service Discovery        |

### Overrides file

The service setting `config_path` names a JSON file of type `api.Config`. A
missing file, or an empty path, leaves the embedded defaults in place.

| JSON key            | Type     | Replaces                       |
|---------------------|----------|--------------------------------|
| `stun_servers`      | []string | `Services.StunServers`         |
| `route_setup_nodes` | []string | `Services.RouteSetupNodes`     |
| `survey_whitelist`  | []string | `Services.SurveyWhitelist`     |
| `transport_setup`   | []string | `Services.TransportSetupPKs`   |

A field absent from the file keeps its default.

## Interfaces

### HTTP API

None of the routes requires authentication.

| Method and path | Response                                                          |
|-----------------|-------------------------------------------------------------------|
| `GET /`         | 200 with the `deployment.Services` document                       |
| `GET /dmsghttp` | 200 with the `httputil.DMSGHTTPConf` document                     |
| `GET /health`   | 200 with `service_name` `config-bootstrapper`, `build_info`, `started_at`, `dmsg_address` |

### CXO Feeds

The publisher (`api.ServicesCXOPublisher`) serves one feed on dmsg port 78,
signed by the bootstrapper's key. The leaf `services/current`
(`deployment.ServicesCXOPath`) holds the gzipped JSON of the `Services` document
that `GET /` returns. The publisher compares the document with the last one it
published every minute (`servicesPublishInterval`) and publishes only when it
changed. The feed is open to any subscriber, and its sequence prevents an older
document from replacing a newer one.

## Behaviour

The served document starts as `deployment.Prod`, with the overrides applied. The
`dmsg_servers` list is refreshed from dmsg discovery at start and then every 5
minutes (`dmsgServersRefreshInterval`), by reading `/dmsg-discovery/all_servers`.
If the answer is empty or the request fails, the previous list is kept.

`GET /dmsghttp` builds its document from the `*_dmsg` addresses of the services
document and from the current server list. The result is cached for 5 minutes.

## Security Considerations

The document is public. Visors that use it trust the dmsg identity of the
bootstrapper and, for the CXO feed, the signature of its key. The document
carries the keys that visors trust for route setup, transport setup and surveys,
so these are changed at the bootstrapper.

## References

- Skywire Visor
- Service Discovery
