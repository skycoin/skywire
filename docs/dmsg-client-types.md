# DMSG Client Types

Skywire components connect to the DMSG network using different client types depending on their role. This document explains the two main approaches and which components use each.

## Direct Client (`direct.NewClient`)

**Used by:** All Skywire services (transport-discovery, route-finder, service-discovery, uptime-tracker, address-resolver, config-bootstrapper)

The direct client is an in-memory implementation of `disc.APIClient` that does **not** communicate with the DMSG discovery service. Instead, it is pre-populated with DMSG server entries and uses them directly to establish connections.

```go
// Service bootstrap pattern:
servers := dmsghttp.GetServers(ctx, dmsgDisc, dmsgServerType, logger)
keys := cipher.PubKeys{pk}
dClient := direct.NewClient(direct.GetAllEntries(keys, servers), logger)
dmsgDC, closeDmsgDC, err := direct.StartDmsg(ctx, logger, pk, sk, dClient, config)
```

Key characteristics:
- **No discovery registration**: `PostEntry()` stores entries in memory only — the service does not appear in DMSG discovery as a client
- **Pre-populated server list**: DMSG servers are provided at initialization, fetched via HTTP from discovery beforehand
- **Serves dmsghttp**: Services call `dmsghttp.ListenAndServe()` to accept HTTP-over-DMSG requests on port 80 (default)
- **One-way connectivity**: The service connects to DMSG servers and listens, but is not discoverable via the discovery service

This means services are reachable at `dmsg://{service-pk}:80` by any DMSG client that knows their public key, but they don't advertise their presence in discovery.

### Source

- Implementation: `vendor/github.com/skycoin/dmsg/pkg/direct/client.go`
- Entry helpers: `vendor/github.com/skycoin/dmsg/pkg/direct/entries.go`
- Startup: `vendor/github.com/skycoin/dmsg/pkg/direct/direct.go` (`StartDmsg()`)

## Regular DMSG Client (`dmsgc.New` via `disc.NewHTTP`)

**Used by:** Visors

The regular DMSG client uses an HTTP-based discovery client that actively registers the visor in DMSG discovery and periodically updates its entry.

```go
// Visor DMSG initialization:
httpC, err := getHTTPClient(ctx, v, v.conf.Dmsg.Discovery)
dmsgC := dmsgc.New(pk, sk, ebc, conf.Dmsg, httpC, masterLogger)
```

Key characteristics:
- **Registers in discovery**: The visor's client entry (with delegated servers) is posted to DMSG discovery via HTTP
- **Periodic updates**: Entry is refreshed periodically so other nodes can find the visor
- **Two-way connectivity**: Visor is both discoverable (via discovery entry) and can discover other nodes
- **Supports sessions**: Manages multiple concurrent sessions to DMSG servers

### Source

- Implementation: `pkg/dmsgc/dmsgc.go`
- Discovery client: `vendor/github.com/skycoin/dmsg/pkg/disc/client.go` (`NewHTTP()`)

## DMSG HTTP Client (Visor subsystem)

**Used by:** Visor's internal `dmsg_http` module

A third pattern exists for the visor's own HTTP-over-DMSG client, which uses a direct client to bootstrap a separate DMSG connection for making HTTP requests to services over DMSG.

```go
// Visor dmsghttp initialization (init_dmsg.go):
entries := direct.GetAllEntries(keys, servers)
dClient := direct.NewClient(entries, logger)
dmsgDC, closeDmsgDC, err := direct.StartDmsg(ctx, logger, pk, sk, dClient, config)
dmsgHTTP := http.Client{Transport: dmsghttp.MakeHTTPTransport(ctx, dmsgDC)}
```

This gives the visor an `http.Client` that routes requests through DMSG, used to reach services at `dmsg://` URLs. The DMSG servers to use for this are taken from the visor's configuration (`conf.Dmsg.Servers`).

### Source

- Initialization: `pkg/visor/init_dmsg.go` (`initDmsgHTTP()`)
- HTTP transport: `vendor/github.com/skycoin/dmsg/pkg/dmsghttp/http_transport.go`

## Summary

| Component | Client Type | Registers in Discovery | Discoverable | Serves dmsghttp |
|-----------|------------|----------------------|-------------|----------------|
| Services (tpd, rf, sd, ut, ar) | Direct | No | By PK only | Yes (port 80) |
| Visor (main DMSG) | Regular | Yes | Yes | No |
| Visor (dmsg_http subsystem) | Direct | No | No | No (client only) |
