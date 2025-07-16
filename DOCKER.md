## Build docker image
```
$ ./ci_scripts/docker-push.sh -t $(git rev-parse --abbrev-ref HEAD) -b
```

### Hypervisor web UI


Docker container will create config automatically for you. To run it manually:

```
 docker run --rm -v <YOUR_CONFIG_DIR>:/opt/skywire \
  skycoin/skywire:test skywire cli config gen -i
```

After starting up the visor, the UI will be exposed by default on `localhost:8000`.

### Add remote hypervisor

from docker image:

```
docker run --rm -v <YOUR_CONFIG_DIR>:/opt/skywire \
  skycoin/skywire:test skywire cli config update hypervisor-pks <public-key>
```

Or from docker image:/* #nosec */

```
docker run --rm -v <YOUR_CONFIG_DIR>:/opt/skywire \
  skycoin/skywire:latest skywire cli update-config hypervisor-pks <public-key>
```

## Run `skywire visor`

from docker image:
```
# with custom config mounted on docker volume
docker run --rm -p 8000:8000 -v <YOUR_CONFIG_DIR>:/opt/skywire --name=skywire skycoin/skywire:test skywire visor -c /opt/skywire/<YOUR_CONFIG_NAME>.json
# without custom config (config is automatically generated)
docker run --rm -p 8000:8000 --name=skywire skycoin/skywire:test skywire visor
```
