---
name: skywire-deployment-services
description: Inspect and debug the skywire deployment services (transport discovery, dmsg discovery, service discovery, route finder, address resolver, config service, dmsg servers) and remote visors over the mesh. Health, pprof and logs over dmsg through the survey-whitelisted SOCKS5 proxy. Load for deployment service health, pprof or logs.
---

# Deployment services and remote visor debugging

## How the services run now
- A deployment service runs embedded in a visor, from an `embedded_services` block in that visor's config. `skywire cli config gen --deployment <host[:port]>` writes a whole deployment into one visor.
- Usually each service has its own key. A service without its own key is mounted on the host visor under a path prefix on the visor's dmsg port 80 (`dmsg://<visor pk>:80/<prefix>`). A mounted service starts and stops with the visor and cannot be controlled apart from it.
- Services bind no plain HTTP listener by default. The `mode` field is `dmsg`, `http` or `dual`. Add an HTTP listener only on purpose.
- `skywire cli visor state --select services` lists the embedded services of a visor and whether each is running. Use it first when a service looks down. `skywire cli visor services start|stop|restart <name>` controls a service that has its own key.
- A visor that hosts services serves a status page on the address in `deployment_status_addr` when that is set.
- The old network monitor subcommand of `svc` was removed. The service set is `svc health`, `svc tpd`, `svc dmsgd` and `svc ar`.
- Everything is dmsg only. The old public HTTP endpoints are gone.

## The survey-whitelist proxy on 127.0.0.1:4443
- A visor can run a resolving SOCKS5 proxy under its survey whitelist key on `127.0.0.1:4443`. Check with `ss -lntp | grep 4443`.
- Only that key can read the `/debug/*` surfaces of services and visors. Other callers get 401. The general resolver on `:4445` is not gated, so use `:4443` for debug paths.
- Fetch with `curl -x socks5h://127.0.0.1:4443 --max-time 25 http://<pk>.dmsg/<path>`. A short timeout shows as HTTP 000 and is usually transient, so retry before concluding an endpoint is missing.

## Health
- `skywire cli svc health` is the fast first look. It prints every deployment service with status, latency, version and public key. Take the keys from there, not from notes.
- One service raw: `curl -x socks5h://127.0.0.1:4443 --max-time 25 http://<svc-pk>.dmsg/health`. Services answer on dmsg port 80.
- One visor: `skywire cli visor info --via dmsg://<pk>` or `skywire cli dmsg curl dmsg://<pk>:80/health`.
- Pin the check through one dmsg server with `skywire cli svc health --service <svc-pk> --dmsg-server <srv-pk[@ip:port]>`. Add `--carriers tcp,ws,wt,quic` to probe one server over each carrier.
- `skywire dmsg conf probe` gives a fleet by carrier reachability matrix.
- A 202 "cannot connect to delegated server" on an unpinned dial is a known flake. Pin the server or retry.
- `svc tpd`, `svc dmsgd` and `svc ar` are the read APIs. See skywire-discovery-cli.

## pprof
Services and visors serve `/debug/pprof/*` on dmsg port 80 to the survey key.
```
P="curl -s -x socks5h://127.0.0.1:4443 --max-time 30"
$P http://<pk>.dmsg/debug/pprof/
$P "http://<pk>.dmsg/debug/pprof/goroutine?debug=1"
$P http://<pk>.dmsg/debug/pprof/heap -o heap.pb.gz
$P "http://<pk>.dmsg/debug/pprof/profile?seconds=15" -o cpu.pb.gz
go tool pprof -http=:0 heap.pb.gz
```
An empty whitelist denies everything with 401 by design. For a visor you can reach by pty, its local pprof is on `localhost:6060` by default.

## Logs
- Deployment services keep a ring buffer at `/debug/log`. An empty reply means an empty buffer, not an error.
- Visors do not serve `/debug/log`. A visor serves `/skywire.log`, `/health`, `/node-info`, `/stats/transports`, `/stats/uptime`, `/stats/services` and pprof on dmsg port 80, to the survey key.
- `skywire cli visor log --via dmsg://<pk>` streams a remote visor log over RPC.
- `skywire cli visor app log <app> <RFC3339 time> --via dmsg://<pk>` gives one app.
- Host logs through pty: `skywire cli pty exec <pk> --transport skynet -- journalctl -u skywire --since '5 min ago'`.

## Traps
- Pass `--max-time 25` or more. Dmsg fetches need route setup.
- A mounted service is part of its host visor, so a visor restart restarts it.
- A service key printed by `svc health` is the current truth. Do not hard code keys.
