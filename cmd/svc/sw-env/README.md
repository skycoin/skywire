# sw-env. Skywire environment generator

## Purpose

In this document, we define environment as a set of configurations,
command-line arguments for launching skywire-services and skywire-visors,
alongside functions to monitor and change state of the aforementioned services.


`sw-env` tool can be used in two modes:

1. partial config generation

- dmsg-server: `go run ./cmd/sw-env msg`
- skywire-visor: `go run ./cmd/sw-env visor`
- setup-node: `go run ./cmd/sw-env setup`
  
2. environment generation:

- public environment: `go run ./cmd/sw-env --public` - generates environment with public skywire-services and 3 visors running on localhost
- local environment: `go run ./cmd/sw-env --local` - generates environment with every service running on localhost
- dockerized: `go run ./cmd/sw-env --docker --network SKY001`  - generates environment with every service running in docker containers with virtual network
