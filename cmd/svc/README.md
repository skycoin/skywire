# skywire svc

The `skywire svc` umbrella exposes the deployment-service binaries
(address-resolver, route-finder, service-discovery, transport-discovery,
uptime-tracker, etc.) under one cobra tree.

Command-line documentation lives at
[/docs/skywire/svc/README.md](../../docs/skywire/svc/README.md) and is
generated from the live cobra tree — run `skywire doc` from the repo
root to regenerate.

Per-service deep documentation (HTTP endpoints, deployment notes, etc.)
remains under each service's own directory:

- [address-resolver/](address-resolver/README.md)
- [config-bootstrapper/](config-bootstrapper/README.md)
- [geoip/](geoip/README.md)
- [network-monitor/](network-monitor/README.md)
- [route-finder/](route-finder/README.md)
- [service-discovery/](service-discovery/README.md)
- [setup-node/](setup-node/README.md)
- [stun-server/](stun-server/README.md)
- [transport-discovery/](transport-discovery/README.md)
