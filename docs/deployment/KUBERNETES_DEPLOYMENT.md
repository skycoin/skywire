# Skywire Kubernetes Deployment

This document describes deploying skywire services on a Kubernetes cluster.

For Docker Compose deployment see [DOCKER_DEPLOYMENT.md](DOCKER_DEPLOYMENT.md).
For systemd / direct-on-host deployment see [DEPLOYMENT_SYSTEMD.md](DEPLOYMENT_SYSTEMD.md).

## Status of this guide

The reference production deployment runs on Docker Compose; there is no Kubernetes deployment in production today, and the manifests in this guide are illustrative — they describe the shape of a working deployment, not a packaged one. Treat this as a starting point for a custom Helm chart or kustomize tree, and expect to iterate. Snippets here are intentionally minimal so the structure stays readable; production manifests will need resource limits, security contexts, NetworkPolicies, monitoring sidecars, and so on.

## When K8s is and isn't a good fit

K8s fits the parts of the deployment that look like ordinary stateless web services — `dmsg-discovery`, `transport-discovery`, `address-resolver` (HTTP side), `service-discovery`, `route-finder`, `uptime-tracker`, `setup-node`, `config-bootstrapper`, `network-monitor`. These are clean Deployments behind ClusterIP Services with an Ingress for external traffic.

K8s gets awkward for two pieces:

- **STUN server.** RFC 3489 NAT detection requires the server to respond from **two distinct public IPv4 addresses** bound on the same machine, in host networking mode. Pods in K8s normally share the node's IP and don't bind raw addresses. Realistic options: run STUN outside the cluster on a dedicated host (the way Docker Compose does it via `network_mode: "host"`), or use a CNI/loadbalancer that can hand each pod a routable secondary IP. There is no clean, portable manifest for this. If you don't need a custom STUN cluster, point visors at any public RFC-compliant STUN server and skip deploying it yourself.

- **`dmsg-server` public address.** Each dmsg-server advertises a `public_address` (e.g. `192.0.2.10:30086`) in `dmsg-discovery`. Visors dial that address directly over TCP — there is no service-mesh hop. In K8s this means each `dmsg-server` replica needs a stable, externally-reachable IP/port pair that matches its `config.json`. A `Service` of type `LoadBalancer` per replica works but is expensive and provider-specific; `NodePort` works if your nodes have public IPs and you pin replicas with `nodeSelector`. Either way, **`public_address` in each replica's `config.json` must match its actual external address** — there is no auto-detection.

- **Address-resolver UDP port (SUDPH).** AR listens on TCP 9093 (HTTP) and UDP 30178 (KCP/SUDPH). Until K8s 1.24+ you needed two separate Services for mixed-protocol; from 1.24+ a single LoadBalancer Service with `protocol: TCP` and `protocol: UDP` ports works on most cloud providers. Either way the AR's `--public-udp-address` flag (added recently) must be set to the externally-reachable host:port that visors will UDP-dial.

If those three constraints don't match your cluster (e.g. no public-IP LoadBalancer, single-NIC nodes), running the data-plane components on a small VM cluster with Docker Compose alongside a K8s control plane for the HTTP services is a reasonable hybrid.

## Topology

A minimal cluster runs the following workloads:

| Workload | Kind | Replicas | External traffic |
|---|---|---|---|
| `redis` | StatefulSet | 1 (or use a managed Redis) | none |
| `postgres` | StatefulSet | 1 (or use a managed DB) | none — only `uptime-tracker` connects |
| `dmsg-discovery` | Deployment | 2+ | Ingress (TLS) → ClusterIP :9090 |
| `address-resolver` | Deployment | 2+ | Ingress (TLS) → ClusterIP :9093, plus LoadBalancer UDP :30178 |
| `transport-discovery` | Deployment | 2+ | Ingress (TLS) → ClusterIP :9094 |
| `service-discovery` | Deployment | 2+ | Ingress (TLS) → ClusterIP :9098 |
| `route-finder` | Deployment | 2+ | Ingress (TLS) → ClusterIP :9092 |
| `uptime-tracker` | Deployment | 2+ | Ingress (TLS) → ClusterIP :9095 |
| `config-bootstrapper` | Deployment | 1 | Ingress (TLS) → ClusterIP :9082 |
| `setup-node` | Deployment | 1+ | none — dmsg-only |
| `network-monitor` | Deployment | 1 | none — internal |
| `dmsg-server-N` | StatefulSet (one per server) | 1 each | LoadBalancer or NodePort with stable external IP/port |
| `geoip` | Deployment | 1 | optional Ingress |

`dmsg-server` is one StatefulSet per server (not one StatefulSet with N replicas) because each server has a unique keypair, a unique advertised `public_address`, and (in the embedded `dmsg.Prod.DmsgServers` list distributed with the binary) a fixed PK that visors expect at a specific dmsg.public-address.

## Secrets and ConfigMaps

Each service that takes a secret key needs one. Keep keys in `Secret` resources, not ConfigMaps:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: skywire-keys
type: Opaque
stringData:
  ar-sk: "<64-hex-chars-from skywire cli config gen-keys>"
  tpd-sk: "<...>"
  sd-sk: "<...>"
  rf-sk: "<...>"
  ut-sk: "<...>"
  dmsgd-sk: "<...>"
  redis-password: "<...>"
  postgres-password: "<...>"
  ut-api-key: "<...>"
```

Whitelisted PKs (network-monitor, survey, etc.) and per-service config files (config-bootstrapper's `config.json`, setup-node config, dmsg-server config minus the secret key) belong in ConfigMaps.

For dmsg-server, mount the public part of the config as a ConfigMap and the secret key as a Secret — assemble them into a single `config.json` at pod start with an init container, or pass the secret key via `--sk` on the command line if the binary supports it.

## Custom services-config.json

The skywire binary embeds a `services-config.json` covering the production deployment's PKs and dmsg-server list. Visors and services use this for bootstrap. **For a custom K8s deployment with its own dmsg-server PKs, override the embedded config via the `SKYDEPLOY` environment variable** pointing at a mounted ConfigMap containing your cluster's services-config.json:

```yaml
env:
  - name: SKYDEPLOY
    value: /etc/skywire/services-config.json
volumeMounts:
  - name: services-config
    mountPath: /etc/skywire
volumes:
  - name: services-config
    configMap:
      name: skywire-services-config
```

The format follows `deployment/services-config.json` in the repo — `prod` and `test` keys, each with `dmsg_servers`, `dmsg_discovery`, `transport_discovery`, etc. **Every component in the cluster (services and visors)** must use the same `services-config.json` for DHT bootstrap to work; mismatched lists mean dmsg-servers won't peer with each other and visors won't find DHT full nodes.

## Redis and Postgres

Use a managed Redis / Postgres if your cloud provider offers one — the operational headache of running them as StatefulSets in your own cluster usually exceeds the cost. If you do run them in-cluster, use `volumeClaimTemplates` for persistent storage:

```yaml
apiVersion: apps/v1
kind: StatefulSet
metadata:
  name: redis
spec:
  serviceName: redis
  replicas: 1
  selector: { matchLabels: { app: redis } }
  template:
    metadata: { labels: { app: redis } }
    spec:
      containers:
        - name: redis
          image: redis:7
          args:
            - --requirepass
            - $(REDIS_PASSWORD)
          env:
            - name: REDIS_PASSWORD
              valueFrom:
                secretKeyRef: { name: skywire-keys, key: redis-password }
          volumeMounts:
            - { name: data, mountPath: /data }
  volumeClaimTemplates:
    - metadata: { name: data }
      spec:
        accessModes: [ReadWriteOnce]
        resources: { requests: { storage: 10Gi } }
```

The same Redis instance can back every service that needs it (`address-resolver`, `transport-discovery`, `dmsg-discovery`, `service-discovery`, `dmsg-server` DHT). The DHT keys live alongside the discovery keys in the same database — see `pkg/dht/mirror_redis.go`.

## A representative service Deployment

Address-resolver, illustrating the env / args pattern shared by every HTTP service:

```yaml
apiVersion: apps/v1
kind: Deployment
metadata: { name: address-resolver }
spec:
  replicas: 2
  selector: { matchLabels: { app: address-resolver } }
  template:
    metadata: { labels: { app: address-resolver } }
    spec:
      containers:
        - name: ar
          image: skycoin/skywire:test
          args:
            - svc
            - ar
            - --addr
            - ":9093"
            - --redis
            - redis://redis:6379
            - --dmsg-disc
            - http://dmsg-discovery:9090
            - --sk
            - $(AR_SK)
            - --public-udp-address
            - "ar.example.com:30178"
            - --whitelist-keys
            - "$(NETWORK_MONITOR_PK)"
            - --entry-timeout
            - 5m
          env:
            - name: REDIS_PASSWORD
              valueFrom: { secretKeyRef: { name: skywire-keys, key: redis-password } }
            - name: AR_SK
              valueFrom: { secretKeyRef: { name: skywire-keys, key: ar-sk } }
            - name: SKYDEPLOY
              value: /etc/skywire/services-config.json
          ports:
            - { name: http, containerPort: 9093 }
            - { name: sudph, containerPort: 30178, protocol: UDP }
          volumeMounts:
            - { name: services-config, mountPath: /etc/skywire }
      volumes:
        - name: services-config
          configMap: { name: skywire-services-config }
---
apiVersion: v1
kind: Service
metadata: { name: address-resolver }
spec:
  selector: { app: address-resolver }
  ports:
    - { name: http, port: 9093, targetPort: http }
---
apiVersion: v1
kind: Service
metadata:
  name: address-resolver-sudph
  annotations:
    # Cloud-specific: ensure the LB is created with UDP support.
    # Examples: service.beta.kubernetes.io/aws-load-balancer-type: "nlb"
spec:
  type: LoadBalancer
  externalTrafficPolicy: Local
  selector: { app: address-resolver }
  ports:
    - { name: sudph, port: 30178, targetPort: sudph, protocol: UDP }
```

Two services on purpose: HTTP behind the cluster Ingress (TLS-terminated by Ingress controller); SUDPH on a `LoadBalancer` Service so the externally-reachable UDP host:port matches `--public-udp-address`. `externalTrafficPolicy: Local` preserves the source IP so AR's `hasAddress` check passes; without it, AR sees the kube-proxy SNAT'd address and rejects the bind.

The other HTTP-only services (TPD, SD, RF, UT, DMSGD, conf-service) follow the same shape, minus the UDP Service.

## DMSG Server StatefulSet

Each dmsg-server runs as its own StatefulSet so it can have a stable PK and a stable external address. Skeleton:

```yaml
apiVersion: apps/v1
kind: StatefulSet
metadata: { name: dmsg-server-1 }
spec:
  serviceName: dmsg-server-1
  replicas: 1
  selector: { matchLabels: { app: dmsg-server-1 } }
  template:
    metadata: { labels: { app: dmsg-server-1 } }
    spec:
      containers:
        - name: dmsg-server
          image: skycoin/skywire:test
          args:
            - dmsg
            - server
            - start
            - /etc/skywire/dmsg-server/config.json
          env:
            - name: REDIS_PASSWORD
              valueFrom: { secretKeyRef: { name: skywire-keys, key: redis-password } }
            - name: TPD_URL
              value: http://transport-discovery:9094
            - name: SD_URL
              value: http://service-discovery:9098
            - name: SKYDEPLOY
              value: /etc/skywire/services-config.json
          ports:
            - { name: dmsg, containerPort: 8080 }
          volumeMounts:
            - { name: dmsg-config, mountPath: /etc/skywire/dmsg-server }
            - { name: services-config, mountPath: /etc/skywire }
      volumes:
        - name: dmsg-config
          # Assemble at start: secret key from Secret + public config from ConfigMap
          # via an init container, OR mount a single Secret containing the full
          # config.json (one Secret per dmsg-server replica).
          secret: { secretName: dmsg-server-1-config }
        - name: services-config
          configMap: { name: skywire-services-config }
---
apiVersion: v1
kind: Service
metadata: { name: dmsg-server-1-public }
spec:
  type: LoadBalancer
  externalTrafficPolicy: Local
  selector: { app: dmsg-server-1 }
  ports:
    - { name: dmsg, port: 30081, targetPort: dmsg }
```

The dmsg-server's `config.json` (stored in `dmsg-server-1-config` Secret) must include:

```json
{
  "public_key": "...",
  "secret_key": "...",
  "discovery": "http://dmsg-discovery:9090",
  "public_address": "203.0.113.10:30081",
  "local_address": ":8080",
  "max_sessions": 2048,
  "enable_dht": true,
  "redis_addr": "redis:6379"
}
```

`public_address` must match the LoadBalancer's external IP/port. There's no in-cluster way to discover this before the LB is provisioned; bring up the Service first, note its external IP, then write that into the config Secret. Custom controllers exist that automate this (e.g. CrossPlane, external-dns), but the manual two-step is the simplest path.

See [DOCKER_DEPLOYMENT.md "DMSG Server DHT"](DOCKER_DEPLOYMENT.md#dmsg-server-dht-optional-recommended-for-production) for what `enable_dht`, `redis_addr`, `TPD_URL`, and `SD_URL` actually do.

## Ingress / TLS

Standard Ingress per service, TLS via cert-manager:

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: skywire-services
  annotations:
    cert-manager.io/cluster-issuer: letsencrypt-prod
spec:
  tls:
    - hosts:
        - dmsgd.example.com
        - ar.example.com
        - tpd.example.com
        - sd.example.com
        - rf.example.com
        - ut.example.com
        - conf.example.com
      secretName: skywire-tls
  rules:
    - host: dmsgd.example.com
      http: { paths: [{ path: /, pathType: Prefix, backend: { service: { name: dmsg-discovery, port: { number: 9090 } } } }] }
    - host: ar.example.com
      http: { paths: [{ path: /, pathType: Prefix, backend: { service: { name: address-resolver, port: { number: 9093 } } } }] }
    # ... and so on for tpd, sd, rf, ut, conf
```

The Ingress only carries HTTP — the AR's UDP service stays separate (LoadBalancer above). Don't proxy UDP through an HTTP Ingress controller.

## Visor configuration

Visors that should use this cluster get a config generated against your conf-service:

```
skywire cli config gen -ip -a conf.example.com -o skywire-config.json
```

Or, for a hand-rolled config, swap the service URLs to your cluster's hostnames. Make sure the visor's binary was built with (or has `SKYDEPLOY` pointing at) the same `services-config.json` the cluster uses, otherwise the dmsg-server PK list will be wrong and DHT bootstrap will fail.

## Pprof / debug access

Each service in the cluster runs pprof on `dmsg port 81`, gated by the `survey_whitelist` baked into the binary or supplied via `SKYDEPLOY`. There is no cluster-side ingress for pprof — it's only reachable over dmsg, by callers whose PK is in the whitelist. This is by design (see PR #2390 series); don't expose `/debug/pprof/` over HTTP Ingress, and don't add Service entries for the dmsg-side pprof port.

For in-cluster Go pprof on individual pods, services accept `--pprof :PORT` to bind a plaintext HTTP pprof handler on a private port. Bind it to localhost or to a ClusterIP-only Service, never to the Ingress.

## Troubleshooting

**dmsg-servers not peering, DHT empty.** Check that every dmsg-server's `public_address` matches its LoadBalancer external address, that all dmsg-server PKs are in the same `services-config.json` mounted into every workload, and that each dmsg-server's `redis_addr` resolves and the password is correct. The dmsg-server's startup log prints "DHT bootstrap succeeded peers=N" when the mesh forms.

**Visors can't register SUDPH.** AR's UDP Service must hand visors a routable host:port that matches `--public-udp-address`. `externalTrafficPolicy: Local` is required so AR sees the visor's real source IP — without it, AR's `hasAddress` check rejects the bind because the kube-proxy SNAT'd address isn't in the visor's claimed local-addresses list.

**STCPR works but SUDPH doesn't.** Same as above, plus check that the cluster's egress doesn't NAT outgoing UDP differently from inbound. SUDPH hole-punching needs symmetric NAT behavior; with strict-symmetric-NAT egress, only STCPR will work.

**Services start but discovery returns empty.** Confirm Redis is reachable from the service pods (try `redis-cli -h redis -a $REDIS_PASSWORD ping` from a debug pod). Confirm the Postgres connection string for uptime-tracker. If service-discovery and transport-discovery are connected to Redis but their `dht:*` mirror is empty, check that they're using the same Redis DB index (`storeconfig.RedisDB` defaults to 0).

**STUN unavailable.** Run STUN outside the cluster (a dedicated VM with two public IPs and `network_mode: host`). Reference its address from the cluster's `services-config.json` `stun_servers` list. There is no portable way to run STUN inside K8s.
