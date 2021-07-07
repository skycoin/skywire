# Skywire Visor Documentation

<!-- MarkdownTOC levels="1,2,3,4,5" autolink="true" bracket="round" -->
- [Install](#install)
- [skywire-visor usage](#skywire-visor-usage)
- [config file generation](#config-file-generation)
	- [Example running from source dir](#example-running-from-source-dir)
	- [Example package based installation](#example-package-based-installation)

<!-- /MarkdownTOC -->


## Install

```bash
$ cd $GOPATH/src/github.com/skycoin/skywire/cmd/skywire-visor
$ go install ./...
```

## skywire-visor usage

After the installation, you can run `skywire-visor -h` to see the usage:

```
$ skywire-visor -h
Skywire visor

Usage:
  skywire-visor [flags]

Flags:
  -c, --config string      config file location. If the value is 'STDIN', config file will be read from stdin.
      --delay string       start delay (deprecated) (default "0ns")
  -h, --help               help for skywire-visor
      --pprofaddr string   pprof http port if mode is 'http' (default "localhost:6060")
  -p, --pprofmode string   pprof profiling mode. Valid values: cpu, mem, mutex, block, trace, http
      --syslog string      syslog server address. E.g. localhost:514
      --tag string         logging tag (default "skywire")
  -v, --version            version for skywire-visor
```

## config file generation

Refer to the skywire-cli documentation for more detailed information regarding additional flags and argument that may be passed to the following command:

```
skywire-cli visor gen-config
```

With no additional flags or arguments, the configuration is written to skywire-config.json and stdout.

##### Example running from source dir

The default filename and paths in the skywire-config.json file are designed for the context of running skywire-visor from within the cloned source repository, wherever it may reside. A brief example of the terminal ouput when running skywire-visor where skywire-config.json exists in the current directory:

```
$ skywire-visor
[2021-06-24T22:08:02-05:00] INFO []: Starting
[2021-06-24T22:08:02-05:00] DEBUG []: Process info delay=0s parent_systemd=false systemd=false
Version "0.4.1" built on "2021-03-19T23:26:21Z" against commit "d804a8ce"
[2021-06-24T22:08:02-05:00] INFO [visor:config]: Reading config from file. filepath="/home/user/go/src/github.com/skycoin/skywire/skywire.json"
[2021-06-24T22:08:02-05:00] INFO [visor:config]: Flushing config to file. config_version="v1.0.0" filepath="/home/user/go/src/github.com/skycoin/skywire/skywire.json"
[2021-06-24T22:08:02-05:00] INFO [visor:startup]: Begin startup. public_key=03e08185a0328b7cfd06b8a6f89d605396217053fa0868872e1dbe77b8bab92e1c
[2021-06-24T22:08:02-05:00] INFO [1/14] [visor:startup:updater]: Starting module...
[2021-06-24T22:08:02-05:00] INFO [1/14] [visor:startup:updater]: Module started successfully. elapsed=21.567µs
[2021-06-24T22:08:02-05:00] INFO [2/14] [visor:startup:eventbroadcaster]: Starting module...
[2021-06-24T22:08:02-05:00] INFO [2/14] [visor:startup:eventbroadcaster]: Module started successfully. elapsed=12.082µs
[2021-06-24T22:08:02-05:00] INFO [3/14] [visor:startup:addressresolver]: Starting module...
[2021-06-24T22:08:02-05:00] INFO [address-resolver]: Remote UDP server: "address.resolver.skywire.skycoin.com:30178"
[2021-06-24T22:08:02-05:00] INFO [3/14] [visor:startup:addressresolver]: Module started successfully. elapsed=42.696µs
[2021-06-24T22:08:02-05:00] INFO [4/14] [visor:startup:discovery]: Starting module...
[2021-06-24T22:08:02-05:00] INFO [4/14] [visor:startup:discovery]: Module started successfully. elapsed=31.709µs
[2021-06-24T22:08:02-05:00] INFO [5/14] [visor:startup:snet]: Starting module...
[2021-06-24T22:08:02-05:00] INFO [snet.dmsgC]: Discovering dmsg servers...
[2021-06-24T22:08:02-05:00] INFO [snet.dmsgC]: Connecting to the dmsg network... timeout=20s
[2021-06-24T22:08:02-05:00] INFO [address-resolver]: BindSUDPR: Address resolver is not ready yet, waiting...
[2021-06-24T22:08:02-05:00] INFO [address-resolver]: BindSTCPR: Address resolver is not ready yet, waiting...
[2021-06-24T22:08:02-05:00] INFO [stcp]: listening on addr: [::]:7777
[2021-06-24T22:08:02-05:00] INFO [address-resolver]: Connected to address resolver. STCPR/SUDPH services are available.
[2021-06-24T22:08:02-05:00] INFO [address-resolver]: BindSUDPR: Address resolver became ready, binding
[2021-06-24T22:08:02-05:00] INFO [address-resolver]: BindSTCPR: Address resolver became ready, binding
[2021-06-24T22:08:02-05:00] INFO [address-resolver]: SUDPH Local port: 57644
[2021-06-24T22:08:02-05:00] INFO [address-resolver]: Performing handshake with 172.104.37.46:30178
[2021-06-24T22:08:03-05:00] INFO [stcpr]: listening on addr: [::]:45999
[2021-06-24T22:08:03-05:00] INFO [snet.dmsgC]: Dialing session... remote_pk=02a49bc0aa1b5b78f638e9189be4ed095bac5d6839c828465a8350f80ac07629c0
```

##### Example for package based installation

Assuming that skywire is installed to /opt/skywire; with app binaries installed at /opt/skywire/apps and the main skywire binaries directly in or symlinked to the executable path. All files generated by running skywire-visor will populate in /opt/skywire

The configuration file is generated in the following way

for a visor with local hypervisor:

```
$ cd /opt/skywire
$ skywire-cli visor gen-config --is-hypervisor -pro skywire.json
```

for visor with remote hypervisor; first copy the existing configuration file to keep the same keys.

```
$ cd /opt/skywire
# cp skywire.json skywire-visor.json
# skywire-cli visor gen-config --hypervisor-pks <remote-hypervisor-public-key> -pro skywire-visor.json
```

These two configuration files can be referenced in systemd service files or init scripts to start skywire with either a local or remote hypervisor.

To clarify some terminology; a 'visor' is a running instance of skywire-visor. Previously, on the testnet, this was called a 'node'. This was changed for the sake of differentiating the hardware from the software when troubleshooting issues. The 'public key' in the configuration file is referred to as the 'visor key' or the visor's public key. In the context of using a visor which is running a hypervisor instance as a remote hypervisor for another visor, the public key (visor key) of the visor running the hypervisor web instance is referred to as the 'hypervisor key' or the hypervisor's public key. A hypervisor key is a visor key, these terms are used interchangeably and refer to the same thing for a visor running a hypervisor instance.

```
# skywire -c /opt/skywire/skywire.json
[2021-06-25T14:48:16-05:00] INFO []: Starting
[2021-06-25T14:48:16-05:00] DEBUG []: Process info delay=0s parent_systemd=false systemd=false
Version "0.4.1" built on "2021-03-19T23:26:21Z" against commit "d804a8ce"
[2021-06-25T14:48:16-05:00] INFO [visor:config]: Reading config from file. filepath="/opt/skywire/skywire.json"
[2021-06-25T14:48:16-05:00] INFO [visor:config]: Flushing config to file. config_version="v1.0.0" filepath="/opt/skywire/skywire.json"
[2021-06-25T14:48:16-05:00] INFO [visor:startup]: Begin startup. public_key=03e08185a0328b7cfd06b8a6f89d605396217053fa0868872e1dbe77b8bab92e1c
[2021-06-25T14:48:16-05:00] INFO [1/14] [visor:startup:updater]: Starting module...
[2021-06-25T14:48:16-05:00] INFO [1/14] [visor:startup:updater]: Module started successfully. elapsed=18.498µs
[2021-06-25T14:48:16-05:00] INFO [2/14] [visor:startup:eventbroadcaster]: Starting module...
[2021-06-25T14:48:16-05:00] INFO [2/14] [visor:startup:eventbroadcaster]: Module started successfully. elapsed=11.675µs
[2021-06-25T14:48:16-05:00] INFO [3/14] [visor:startup:addressresolver]: Starting module...
[2021-06-25T14:48:16-05:00] INFO [address-resolver]: Remote UDP server: "address.resolver.skywire.skycoin.com:30178"
[2021-06-25T14:48:16-05:00] INFO [3/14] [visor:startup:addressresolver]: Module started successfully. elapsed=40.618µs
[2021-06-25T14:48:16-05:00] INFO [4/14] [visor:startup:discovery]: Starting module...
[2021-06-25T14:48:16-05:00] INFO [4/14] [visor:startup:discovery]: Module started successfully. elapsed=13.03µs
[2021-06-25T14:48:16-05:00] INFO [5/14] [visor:startup:snet]: Starting module...
[2021-06-25T14:48:16-05:00] INFO [snet.dmsgC]: Discovering dmsg servers...
[2021-06-25T14:48:16-05:00] INFO [snet.dmsgC]: Connecting to the dmsg network... timeout=20s
[2021-06-25T14:48:16-05:00] INFO [address-resolver]: BindSUDPR: Address resolver is not ready yet, waiting...
[2021-06-25T14:48:16-05:00] INFO [address-resolver]: BindSTCPR: Address resolver is not ready yet, waiting...
[2021-06-25T14:48:16-05:00] INFO [stcp]: listening on addr: [::]:7777
[2021-06-25T14:48:16-05:00] INFO [address-resolver]: Connected to address resolver. STCPR/SUDPH services are available.
[2021-06-25T14:48:16-05:00] INFO [address-resolver]: BindSUDPR: Address resolver became ready, binding
[2021-06-25T14:48:16-05:00] INFO [address-resolver]: BindSTCPR: Address resolver became ready, binding
[2021-06-25T14:48:16-05:00] INFO [address-resolver]: SUDPH Local port: 40418
[2021-06-25T14:48:16-05:00] INFO [address-resolver]: Performing handshake with 172.104.37.46:30178
[2021-06-25T14:48:16-05:00] INFO [stcpr]: listening on addr: [::]:35001
[2021-06-25T14:48:28-05:00] INFO [snet.dmsgC]: Dialing session... remote_pk=02a49bc0aa1b5b78f638e9189be4ed095bac5d6839c828465a8350f80ac07629c0
```
