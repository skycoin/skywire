# Running a visor in Docker

Running the visor itself in a container. For deploying the *deployment
services* — transport-discovery, route-finder, service-discovery,
address-resolver, setup-node, STUN — with Docker Compose, see
[Docker deployment](../deployment/DOCKER_DEPLOYMENT.md) instead.

## Build the image

```
make docker-build
```

That runs `docker/docker_build.sh prod`. `make docker-build-test` builds the
`:test` tag, which is what the [docker auto-pull](../packaging/auto-update.md)
path tracks.

## Run the visor

```
# without a custom config — one is generated on first start
docker run --rm -p 8000:8000 --name=skywire skycoin/skywire:test skywire visor

# with a config mounted from the host
docker run --rm -p 8000:8000 -v <YOUR_CONFIG_DIR>:/opt/skywire --name=skywire \
  skycoin/skywire:test skywire visor -c /opt/skywire/<YOUR_CONFIG_NAME>.json
```

`-p 8000:8000` publishes the hypervisor web UI. It is only served if the
config was generated with `-i` / `--ishv`; see
[configuration](configuration.md).

## Generate or change a config

The container generates a config for you on first start. To produce one
yourself, or to change an existing one, run the CLI in the same image against
a mounted config directory:

```
# a config with the local hypervisor UI enabled
docker run --rm -v <YOUR_CONFIG_DIR>:/opt/skywire \
  skycoin/skywire:test skywire cli config gen -i

# add a remote hypervisor to an existing config
docker run --rm -v <YOUR_CONFIG_DIR>:/opt/skywire \
  skycoin/skywire:test skywire cli config update hv --add-pks <public-key>
```

Note this is `config gen`, not `skywire autoconfig`: there is no systemd unit
and no `/etc/skywire.conf` in the container, so nothing regenerates the config
behind you — the container's config directory is the durable record. See
[which one to use](configuration.md#which-one-autoconfig-or-config-gen).
