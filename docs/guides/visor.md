# Running `skywire visor`

`skywire visor` hosts apps and is an applications gateway to the
Skywire network. It requires a valid configuration to be provided.

_Note: Root permissions or special capabilities are required for VPN
client and VPN server apps — see [permissions.md](permissions.md)._

Run the visor:
```
skywire visor -c skywire-config.json
```
If the default `skywire-config.json` exists in the current dir, this can be shortened to:
```
skywire visor
```

When running VPN applications, ensure proper permissions are configured:
```bash
# With setcap configured (recommended):
skywire visor

# Or with sudo if setcap is not configured:
sudo skywire visor
```

`skywire visor` can be run on Windows. The setup requires additional
setup steps specified in the [Windows setup docs](https://github.com/skycoin/skywire/wiki/Windows-Setup)
if not using the windows .msi installer.

## Process control

In the instance that the visor shuts down or stops, it is desirable to restart it automatically. This may be accomplished in a few different ways.

The visor may be run as a systemd service - on linux. The `skywire.service` does just that, and is provided by the linux packages. `skywire.service` is configured to run as root.

The visor may be run directly in a (bash / zsh / git-bash) terminal using a `while` loop with a short timeout between restarts. If one is using the default config path for package-based installation (i.e. starting the visor with the `-p` flag) it will be required to run the visor as root. If not, omit the `-p` flag and skip becoming root.

On windows it may be required to start the git-bash terminal as admin or superuser to obtain elevated privileges.

To become root on linux:

```
su - root
```

Start the Visor in a `while` loop

```
while true ; do skywire visor -pl debug ; sleep 5 ; done
```
or
```
while true ; do go run github.com/skycoin/skywire@develop visor -pl debug ; sleep 5 ; done
```

A few observations when running the visor in the latter way:

* the config is not updated when the binary is updated
* the binary executed by the visor to start the apps is not the same one as is being `go run`

To address these two things, it's recommended to first create the human-editable skywire.conf file. This may be done with any path, but for this example, the default path referenced by the autoconfig script included with the linux packages is used.

```
go run github.com/skycoin/skywire@develop cli config gen -q | sudo tee /etc/skywire.conf
```

Now, edit `/etc/skywire.conf` as desired.
Remember to uncomment:
* `PKGENV=true`
* `BESTPROTO=true`

Save the file. Now `config gen` can be added to the while loop

```
while true ; do SKYENV=/etc/skywire.conf go run github.com/skycoin/skywire@develop cli config gen -r && go run github.com/skycoin/skywire@develop visor -pl debug ; sleep 5 ; done
```

To update the binary included with the linux packages so that the visor apps will also be running on the latest commits, it's recommended to `go install github.com/skycoin/skywire@develop` and then replace the binary provided by the package with that one. The complete command becomes:

```
while true ; do go install github.com/skycoin/skywire@develop && mv $GOPATH/bin/skywire /opt/skywire/bin/skywire && SKYENV=/etc/skywire.conf skywire cli config gen -r && skywire visor -pl debug ; sleep 5 ; done
```

Note that the binary at: `/opt/skywire/bin/skywire` is already symlinked to `/usr/bin/skywire` by installing the package.

## Transport setup

_Note: transports should be set up automatically when starting an app in most cases. The user should not need to do this manually._

A Transport represents a bidirectional line of communication between two Skywire Visors — see the [Transports wiki page](https://github.com/skycoin/skywire/wiki/Transports).

Transports are automatically established when a client application connects to a server application.
Their creation is attempted in the following order:
- stcpr
- sudph
- dmsg

Transports can be manually created. Existing suitable transports will be automatically used by client applications when they are started.

To create a transport, first copy the public key of an online visor from the uptime tracker:
```
skywire cli ut -o
```
or service discovery:
```
skywire cli vpn list #list vpn server keys
skywire cli proxy list #list proxy server keys
```

Add the transport:
```
skywire cli visor tp add -t <transport-type> <public-key>
```

View established transports:
```
skywire cli visor tp ls
```

Remove a transport:
```
skywire cli visor tp rm -i <transport-id>
```

## Files and folders created by skywire at runtime

_Note: not all of these files will be created by default._
```
├──skywire-config.json
└─┬local
  ├── apps-pid.txt
  ├── node-info.json
  ├── node-info.sha
  ├── reward.txt
  ├── skychat
  ├── skychat_log.db
  ├── skysocks
  ├── skysocks-client
  ├── skysocks-client_log.db
  ├── skysocks_log.db
  └── transport_logs
      ├── 2023-03-06.csv
      ├── 2023-03-07.csv
      ├── 2023-03-08.csv
      ├── 2023-03-09.csv
      └── 2023-03-10.csv
```

Some of these files are served via the [dmsghttp logserver](https://github.com/skycoin/skywire/wiki/DMSGHTTP-logserver).
