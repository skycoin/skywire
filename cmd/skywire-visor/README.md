# Skywire Visor Documentation

<!-- MarkdownTOC levels="1,2,3,4,5" autolink="true" bracket="round" -->
- [Install](#install)
- [skywire-visor usage](#skywire-visor-usage)
- [config file generation](#config-file-generation)
	- [Run from source without compiling](#run-from-source-without-compiling)
	- [Example running from source dir](#example-running-from-source-dir)
	- [Example package based installation](#example-package-based-installation)

<!-- /MarkdownTOC -->


## Install

```bash
$ cd $GOPATH/src/github.com/skycoin/skywire/cmd/skywire-visor
$ go install ./...
```

or

```
make install
```

## Skywire-visor usage

After the installation, you can run `skywire-visor -h` to see the usage:

```
$ skywire-visor --help

	┌─┐┬┌─┬ ┬┬ ┬┬┬─┐┌─┐
	└─┐├┴┐└┬┘││││├┬┘├┤
	└─┘┴ ┴ ┴ └┴┘┴┴└─└─┘

Usage:
  skywire-visor [flags]

Flags:
  -c, --config string   config file to use (default): skywire-config.json
  -b, --browser         open hypervisor ui in default web browser
  -i, --hvui            run as hypervisor
  -u, --user            use config at: $HOME/skywire-config.json
      --all             show all flags
  -h, --help            help for skywire-visor
  -v, --version         version for skywire-visor

  $ skywire-visor --all

  	┌─┐┬┌─┬ ┬┬ ┬┬┬─┐┌─┐
  	└─┐├┴┐└┬┘││││├┬┘├┤
  	└─┘┴ ┴ ┴ └┴┘┴┴└─└─┘

  Usage:
    skywire-visor [flags]

  Flags:
    -c, --config string       config file to use (default): skywire-config.json
    -i, --hvui                run as hypervisor
    -j, --hv string           add remote hypervisor PKs at runtime
    -k, --xhv                 disable remote hypervisors set in config file
    -n, --stdin               read config from stdin
    -p, --pkg                 use package config /opt/skywire/skywire.json
    -q, --pprofmode string    pprof mode: cpu, mem, mutex, block, trace, http
    -r, --pprofaddr string    pprof http port (default "localhost:6060")
    -t, --tag string          logging tag (default "skywire")
	-u, --user                use config at: $HOME/skywire-config.json
    -y, --syslog string       syslog server address. E.g. localhost:514
    -z, --completion string   [ bash | zsh | fish | powershell ]
    -h, --help                help for skywire-visor
    -v, --version             version for skywire-visor

```

## Config file generation

Refer to the [skywire-cli documentation](../skywire-cli/README.md) for more detailed information regarding additional flags and argument that may be passed to the following command:

```
skywire-cli config gen
```

With no additional flags or arguments, the configuration is written to skywire-config.json and stdout.

## Run from source without compiling

The bin_path field in the visor config specifies the path where the binaries are called by skywire-visor. default `./apps`

By placing scripts at this paths with the name of the binary, it is possible to run skywire from source without compiling.

An example of the script

```
#!/bin/bash
go run ../../cmd/apps/skychat/chat.go
```

The following runs skywire from source without compiling:

```
$ ln -sf scripts/_apps apps
$ chmod +x apps/*
$ go run cmd/skywire-cli/skywire-cli.go config gen -ibr skywire-config.json
$ go run cmd/skywire-visor/skywire-visor.go -c skywire-config.json
```

Or, with `make`:

```
make run-source
```

## Run from source without compiling or writing config

Its possible to output the config from skywire-cli to stdout and pipe the config to skywire-visor as stdin

```
go run cmd/skywire-cli/skywire-cli.go config gen -ibn | go run cmd/skywire-visor/skywire-visor.go -nb
```

Useful for testing both skywire-cli and skywire-visor providing a single line to the shell.
The config is not written to file in the above example


##### Example running from source dir

The default filename and paths in the skywire-config.json file are designed for the context of running skywire-visor from within the cloned source repository, wherever it may reside. A brief example of the terminal ouput when running skywire-visor where skywire-config.json exists in the current directory:

```
$ skywire-visor
[2022-05-26T18:28:31-05:00] INFO [visor:config]: Reading config from file.
[2022-05-26T18:28:31-05:00] INFO [visor:config]: filepath="skywire-config.json"
[2022-05-26T18:28:31-05:00] INFO [visor:config]: config version: ="v1.0.0"
[2022-05-26T18:28:31-05:00] INFO [visor:startup]: Begin startup. public_key=02bc601c45633f98da260946936b409ee098609f1192ff2b88986308f100edb721
[2022-05-26T18:28:31-05:00] INFO [visor]: Starting
[2022-05-26T18:28:31-05:00] INFO [stcpr]: Starting
[2022-05-26T18:28:31-05:00] INFO [transport]: Starting
[2022-05-26T18:28:31-05:00] INFO [updater]: Starting
[2022-05-26T18:28:31-05:00] INFO [event_broadcaster]: Starting
[2022-05-26T18:28:31-05:00] INFO [event_broadcaster]: Initialized in 3.274µs (3.457µs with dependencies)
[2022-05-26T18:28:31-05:00] INFO [transport_setup]: Starting
[2022-05-26T18:28:31-05:00] INFO [cli]: Starting
[2022-05-26T18:28:31-05:00] INFO [launcher]: Starting
[2022-05-26T18:28:31-05:00] INFO [dmsg_pty]: Starting
[2022-05-26T18:28:31-05:00] INFO [transports]: Starting
[2022-05-26T18:28:31-05:00] INFO [updater]: Initialized in 3.673µs (4.675µs with dependencies)
[2022-05-26T18:28:31-05:00] INFO [stun_client]: Starting
[2022-05-26T18:28:31-05:00] INFO [address_resolver]: Starting
[2022-05-26T18:28:31-05:00] INFO [public_autoconnect]: Starting
[2022-05-26T18:28:31-05:00] INFO [stcp]: Starting
[2022-05-26T18:28:31-05:00] INFO [router]: Starting
[2022-05-26T18:28:31-05:00] INFO [sudph]: Starting
[2022-05-26T18:28:31-05:00] INFO [public_visor]: Starting
[2022-05-26T18:28:31-05:00] INFO [dmsg_ctrl]: Starting
[2022-05-26T18:28:31-05:00] INFO [hypervisors]: Starting
[2022-05-26T18:28:31-05:00] INFO [uptime_tracker]: Starting
[2022-05-26T18:28:31-05:00] INFO [dmsg_http]: Starting
[2022-05-26T18:28:31-05:00] INFO [dmsg_http]: Initialized in 668ns (921ns with dependencies)
[2022-05-26T18:28:31-05:00] INFO [address_resolver]: Remote UDP server: "ar.skywire.skycoin.com:30178"
[2022-05-26T18:28:31-05:00] INFO [address_resolver]: Initialized in 42.661µs (181.698µs with dependencies)
[2022-05-26T18:28:31-05:00] INFO [discovery]: Starting
[2022-05-26T18:28:31-05:00] INFO [dmsg]: Starting
[2022-05-26T18:28:31-05:00] INFO [discovery]: Initialized in 17.296µs (23.261µs with dependencies)
[2022-05-26T18:28:31-05:00] INFO [dmsg]: Initialized in 22.855µs (26.421µs with dependencies)
[2022-05-26T18:28:31-05:00] INFO [dmsgC]: Discovering dmsg servers...
[2022-05-26T18:28:31-05:00] INFO [hypervisors]: Initialized in 1.249µs (210.904µs with dependencies)
[2022-05-26T18:28:31-05:00] INFO [dmsg_pty]: Initialized in 190.694µs (619.182µs with dependencies)
[2022-05-26T18:28:31-05:00] INFO [cli]: Initialized in 1.331572ms (1.331736ms with dependencies)
[2022-05-26T18:28:31-05:00] INFO [dmsgC]: Dialing session... remote_pk=0371ab4bcff7b121f4b91f6856d6740c6f9dc1fe716977850aeb5d84378b300a13
[2022-05-26T18:28:31-05:00] INFO [uptime_tracker]: Initialized in 463.92504ms (463.98156ms with dependencies)
[2022-05-26T18:28:31-05:00] INFO [address_resolver]: Connected to address resolver. STCPR/SUDPH services are available.
[2022-05-26T18:28:31-05:00] INFO [transport]: Initialized in 500.582129ms (500.948023ms with dependencies)
[2022-05-26T18:28:31-05:00] INFO [transport_manager]: Serving stcpr network
[2022-05-26T18:28:31-05:00] INFO [transport_manager]: listening on network: stcpr
[2022-05-26T18:28:31-05:00] INFO [transport_manager]: transport manager is serving.
[2022-05-26T18:28:31-05:00] INFO [public_autoconnect]: Initialized in 12.59µs (500.899133ms with dependencies)
[2022-05-26T18:28:31-05:00] INFO [stcpr]: Initialized in 103.316µs (501.13456ms with dependencies)
[2022-05-26T18:28:31-05:00] INFO [transport_manager]: Serving stcp network
[2022-05-26T18:28:31-05:00] INFO [transport_setup]: Initialized in 186.376µs (501.130125ms with dependencies)
[2022-05-26T18:28:31-05:00] INFO [dmsgC]: Connecting to the dmsg network... timeout=20s
[2022-05-26T18:28:31-05:00] INFO [stcp]: listening on addr: [::]:7777
[2022-05-26T18:28:31-05:00] INFO [router]: Starting router
[2022-05-26T18:28:31-05:00] INFO [router]: Initialized in 376.886µs (501.178174ms with dependencies)
[2022-05-26T18:28:31-05:00] INFO [transport_setup]: Connecting to the dmsg network. local_pk=02bc601c45633f98da260946936b409ee098609f1192ff2b88986308f100edb721
[2022-05-26T18:28:31-05:00] INFO [transport_manager]: listening on network: stcp
[2022-05-26T18:28:31-05:00] INFO [stcp]: Initialized in 616.148µs (501.542943ms with dependencies)
[2022-05-26T18:28:31-05:00] INFO [stcpr]: Not binding STCPR: no public IP address found
[2022-05-26T18:28:31-05:00] INFO [launcher]: Initialized in 17.554437ms (518.898292ms with dependencies)
[2022-05-26T18:28:31-05:00] INFO [proc_manager]: Accepted proc conn. hello="{"proc_key":"d701a971bfe2416b8eb3a0363a193d09"}" remote=127.0.0.1:38028
[2022-05-26T18:28:31-05:00] INFO (STDOUT) [proc:skychat:d701a971bfe2416b8eb3a0363a193d09]: Version "v1.0.0-297-gb7924dc2" built on "2022-05-03T15:11:12Z" against commit "b7924dc2f23c0c11952d2ef5785d65771cc41904"
[2022-05-26T18:28:31-05:00] INFO [proc:skychat:d701a971bfe2416b8eb3a0363a193d09]: Associated and serving proc conn.
[2022-05-26T18:28:31-05:00] INFO (STDOUT) [proc:skysocks:8cd2310be0bb4566839e326ef4f9be78]: Version "v1.0.0-297-gb7924dc2" built on "2022-05-03T15:11:12Z" against commit "b7924dc2f23c0c11952d2ef5785d65771cc41904"
[2022-05-26T18:28:31-05:00] INFO [proc:skychat:d701a971bfe2416b8eb3a0363a193d09]: Request processed. _elapsed="4.630355ms" _method="Listen" _received="6:28PM" input=02bc601c45633f98da260946936b409ee098609f1192ff2b88986308f100edb721:1 output=0xc00034019e
[2022-05-26T18:28:31-05:00] INFO [proc_manager]: Accepted proc conn. hello="{"proc_key":"8cd2310be0bb4566839e326ef4f9be78"}" remote=127.0.0.1:38040
[2022-05-26T18:28:31-05:00] INFO [proc:skysocks:8cd2310be0bb4566839e326ef4f9be78]: Associated and serving proc conn.
[2022-05-26T18:28:31-05:00] INFO [proc:skysocks:8cd2310be0bb4566839e326ef4f9be78]: Request processed. _elapsed="1.942995ms" _method="Listen" _received="6:28PM" input=02bc601c45633f98da260946936b409ee098609f1192ff2b88986308f100edb721:3 output=0xc0005123be
[2022-05-26T18:28:31-05:00] INFO (STDOUT) [proc:skysocks:8cd2310be0bb4566839e326ef4f9be78]: Starting serving proxy server
[2022-05-26T18:28:32-05:00] INFO [public_visor]: Initialized in 792.229957ms (1.293168941s with dependencies)
[2022-05-26T18:28:32-05:00] INFO [visor]: Initialized in 378ns (1.299971183s with dependencies)
[2022-05-26T18:28:32-05:00] INFO [transport_setup]: Connected! local_pk=02bc601c45633f98da260946936b409ee098609f1192ff2b88986308f100edb721
[2022-05-26T18:28:32-05:00] INFO [dmsgC]: Serving session. remote_pk=0371ab4bcff7b121f4b91f6856d6740c6f9dc1fe716977850aeb5d84378b300a13
[2022-05-26T18:28:32-05:00] INFO [dmsgC]: Connected to the dmsg network. timeout=20s
[2022-05-26T18:28:32-05:00] INFO [transport_setup]: starting listener dmsg_port=47
[2022-05-26T18:28:32-05:00] INFO [transport_manager]: Serving dmsg network
[2022-05-26T18:28:32-05:00] INFO [transport_setup]: Accepting dmsg streams. dmsg_port=47
[2022-05-26T18:28:32-05:00] INFO [transport_manager]: listening on network: dmsg
[2022-05-26T18:28:32-05:00] INFO [dmsg_ctrl]: Initialized in 855.389535ms (1.356181185s with dependencies)
^C[2022-05-26T18:28:36-05:00] INFO []: Closing with received signal. signal=interrupt
[2022-05-26T18:28:36-05:00] INFO [transport_setup]: Dmsg client stopped serving. error="dmsg error 200 - local entity closed"
[2022-05-26T18:28:36-05:00] INFO [visor:shutdown]: Begin shutdown.
[2022-05-26T18:28:36-05:00] INFO [proc:skysocks:8cd2310be0bb4566839e326ef4f9be78]: Request processed. _elapsed="9.991µs" _method="CloseListener" _received="6:28PM" input=0xc00041ae8c
[2022-05-26T18:28:36-05:00] INFO [proc:skysocks:8cd2310be0bb4566839e326ef4f9be78]: Request processed. _elapsed="4.505871873s" _method="Accept" _received="6:28PM" error="listening on closed connection" input=0xc000512a44 output=&{Remote:000000000000000000000000000000000000000000000000000000000000000000:~ ConnID:0}
[2022-05-26T18:28:36-05:00] INFO [proc:skychat:d701a971bfe2416b8eb3a0363a193d09]: Request processed. _elapsed="4.512633994s" _method="Accept" _received="6:28PM" error="listening on closed connection" input=0xc0005128a8 output=&{Remote:000000000000000000000000000000000000000000000000000000000000000000:~ ConnID:0}
[2022-05-26T18:28:36-05:00] INFO [12/12] [visor:shutdown:dmsgctrl]: Shutting down module...
[2022-05-26T18:28:36-05:00] INFO [12/12] [visor:shutdown:dmsgctrl]: Module stopped cleanly. elapsed=6.559041ms
[2022-05-26T18:28:36-05:00] INFO [11/12] [visor:shutdown:launcher.proc_manager]: Shutting down module...
[2022-05-26T18:28:36-05:00] INFO [proc_manager]: App stopped successfully. app_name="skysocks"
[2022-05-26T18:28:36-05:00] INFO [11/12] [visor:shutdown:launcher.proc_manager]: Module stopped cleanly. elapsed=5.18171ms
[2022-05-26T18:28:36-05:00] INFO [10/12] [visor:shutdown:router.serve]: Shutting down module...
[2022-05-26T18:28:36-05:00] INFO [router]: Closing all App connections and RouteGroups
[2022-05-26T18:28:36-05:00] INFO serveRouteGroup [launcher]: Stopped accepting routes. _=skynet error="accept skynet: use of closed network connection"
[2022-05-26T18:28:36-05:00] INFO [router]: Setup client stopped serving. error="dmsg error 200 - local entity closed"
[2022-05-26T18:28:36-05:00] INFO [10/12] [visor:shutdown:router.serve]: Module stopped cleanly. elapsed=5.466709ms
[2022-05-26T18:28:36-05:00] INFO [9/12] [visor:shutdown:transport_setup.rpc]: Shutting down module...
[2022-05-26T18:28:36-05:00] INFO [9/12] [visor:shutdown:transport_setup.rpc]: Module stopped cleanly. elapsed=2.734501ms
[2022-05-26T18:28:36-05:00] INFO [8/12] [visor:shutdown:transport.manager]: Shutting down module...
[2022-05-26T18:28:36-05:00] INFO [transport_manager]: transport manager is closing.
[2022-05-26T18:28:36-05:00] INFO [stcp]: Cleanly stopped serving.
[2022-05-26T18:28:36-05:00] INFO [transport_manager]: transport manager closed.
[2022-05-26T18:28:36-05:00] INFO [router]: Stopped reading packets error="transport is no longer being served"
[2022-05-26T18:28:36-05:00] INFO [8/12] [visor:shutdown:transport.manager]: Module stopped cleanly. elapsed=10.918059ms
[2022-05-26T18:28:36-05:00] INFO [7/12] [visor:shutdown:uptime_tracker]: Shutting down module...
[2022-05-26T18:28:36-05:00] INFO [7/12] [visor:shutdown:uptime_tracker]: Module stopped cleanly. elapsed=2.832327ms
[2022-05-26T18:28:36-05:00] INFO [6/12] [visor:shutdown:cli.listener]: Shutting down module...
2022/05/26 18:28:36 rpc.Serve: accept:accept tcp 127.0.0.1:3435: use of closed network connection
[2022-05-26T18:28:36-05:00] INFO [6/12] [visor:shutdown:cli.listener]: Module stopped cleanly. elapsed=4.315407ms
[2022-05-26T18:28:36-05:00] INFO [5/12] [visor:shutdown:router.serve]: Shutting down module...
[2022-05-26T18:28:36-05:00] INFO [dmsg_pty:cli-server]: Cleanly stopped serving.
[2022-05-26T18:28:36-05:00] INFO [5/12] [visor:shutdown:router.serve]: Module stopped cleanly. elapsed=5.611311ms
[2022-05-26T18:28:36-05:00] INFO [4/12] [visor:shutdown:router.serve]: Shutting down module...
[2022-05-26T18:28:36-05:00] INFO [dmsg_pty]: Serve() ended. error=<nil>
[2022-05-26T18:28:36-05:00] INFO [dmsg_pty]: Cleanly stopped serving. error="dmsg error 200 - local entity closed"
[2022-05-26T18:28:36-05:00] INFO [4/12] [visor:shutdown:router.serve]: Module stopped cleanly. elapsed=7.898296ms
[2022-05-26T18:28:36-05:00] INFO [3/12] [visor:shutdown:dmsg]: Shutting down module...
[2022-05-26T18:28:36-05:00] INFO [dmsgC]: Stopped serving client!
[2022-05-26T18:28:36-05:00] INFO [dmsgC]: Stopped accepting streams. error="session shutdown" session=0371ab4bcff7b121f4b91f6856d6740c6f9dc1fe716977850aeb5d84378b300a13
[2022-05-26T18:28:36-05:00] INFO [dmsgC]: Session closed. error=<nil>
[2022-05-26T18:28:36-05:00] INFO [dmsgC]: All sessions closed.
[2022-05-26T18:28:36-05:00] INFO [transport_manager]: Dmsg client stopped serving. error="dmsg error 200 - local entity closed"
[2022-05-26T18:28:36-05:00] INFO [3/12] [visor:shutdown:dmsg]: Module stopped cleanly. elapsed=682.731919ms
[2022-05-26T18:28:36-05:00] INFO [2/12] [visor:shutdown:address_resolver]: Shutting down module...
[2022-05-26T18:28:36-05:00] INFO [2/12] [visor:shutdown:address_resolver]: Module stopped cleanly. elapsed=2.856591ms
[2022-05-26T18:28:36-05:00] INFO [1/12] [visor:shutdown:event_broadcaster]: Shutting down module...
[2022-05-26T18:28:36-05:00] INFO [1/12] [visor:shutdown:event_broadcaster]: Module stopped cleanly. elapsed=3.133005ms
[2022-05-26T18:28:36-05:00] INFO [visor:shutdown]: Shutdown complete. Goodbye!
[2022-05-26T18:28:36-05:00] INFO []: Visor closed early.

```

##### Example for package based installation

Assuming that skywire is installed to /opt/skywire; with app binaries installed at /opt/skywire/apps and the skywire cli and visor binaries directly in or symlinked to the executable path. All files generated by running skywire-visor should populate in /opt/skywire

The configuration file is generated in the following way

for a visor with local hypervisor:

```
$   skywire-cli config gen -bipr
```

for visor with remote hypervisor:

```
# skywire-cli config gen -bpj <remote-hypervisor-public-key> -o /opt/skywire/skywire-visor.json
```

This configuration file is referenced in systemd service files, to start skywire with either a local or remote hypervisor.

To clarify some terminology; a 'visor' is a running instance of skywire-visor. Previously, on the testnet, this was called a 'node'. This was changed for the sake of differentiating the hardware from the software when troubleshooting issues. The 'public key' in the configuration file is referred to as the 'visor key' or the visor's public key. In the context of using a visor which is running a hypervisor instance as a remote hypervisor for another visor, the public key (visor key) of the visor running the hypervisor web instance is referred to as the 'hypervisor key' or the hypervisor's public key. A hypervisor key is a visor key, these terms are used interchangeably and refer to the same thing for a visor running a hypervisor instance.

```
# skywire-visor -p
[2022-05-26T18:25:53-05:00] INFO [visor:config]: Reading config from file.
[2022-05-26T18:25:53-05:00] INFO [visor:config]: filepath="/opt/skywire/skywire.json"
[2022-05-26T18:25:53-05:00] INFO [visor:config]: config version: ="1.0.0"
[2022-05-26T18:25:53-05:00] INFO [visor:startup]: Begin startup. public_key=022769f159bb6f67c13140a214d5da0f2192d0881f00449460c458e17eb60115a7
[2022-05-26T18:25:53-05:00] INFO [hypervisor]: Starting
[2022-05-26T18:25:53-05:00] INFO [visor]: Starting
[2022-05-26T18:25:53-05:00] INFO [event_broadcaster]: Starting
[2022-05-26T18:25:53-05:00] INFO [event_broadcaster]: Initialized in 2.908µs (3.293µs with dependencies)
[2022-05-26T18:25:53-05:00] INFO [transports]: Starting
[2022-05-26T18:25:53-05:00] INFO [uptime_tracker]: Starting
[2022-05-26T18:25:53-05:00] INFO [discovery]: Starting
[2022-05-26T18:25:53-05:00] INFO [dmsg_http]: Starting
[2022-05-26T18:25:53-05:00] INFO [transport_setup]: Starting
[2022-05-26T18:25:53-05:00] INFO [launcher]: Starting
[2022-05-26T18:25:53-05:00] INFO [router]: Starting
[2022-05-26T18:25:53-05:00] INFO [updater]: Starting
[2022-05-26T18:25:53-05:00] INFO [updater]: Initialized in 1.996µs (2.178µs with dependencies)
[2022-05-26T18:25:53-05:00] INFO [dmsg_ctrl]: Starting
[2022-05-26T18:25:53-05:00] INFO [stun_client]: Starting
[2022-05-26T18:25:53-05:00] INFO [hypervisors]: Starting
[2022-05-26T18:25:53-05:00] INFO [stcpr]: Starting
[2022-05-26T18:25:53-05:00] INFO [public_visor]: Starting
[2022-05-26T18:25:53-05:00] INFO [public_autoconnect]: Starting
[2022-05-26T18:25:53-05:00] INFO [dmsg_http]: Initialized in 647ns (847ns with dependencies)
[2022-05-26T18:25:53-05:00] INFO [discovery]: Initialized in 12.361µs (264.616µs with dependencies)
[2022-05-26T18:25:53-05:00] INFO [dmsg_pty]: Starting
[2022-05-26T18:25:53-05:00] INFO [transport]: Starting
[2022-05-26T18:25:53-05:00] INFO [stcp]: Starting
[2022-05-26T18:25:53-05:00] INFO [cli]: Starting
[2022-05-26T18:25:53-05:00] INFO [dmsg]: Starting
[2022-05-26T18:25:53-05:00] INFO [sudph]: Starting
[2022-05-26T18:25:53-05:00] INFO [address_resolver]: Starting
[2022-05-26T18:25:53-05:00] INFO [dmsgC]: Discovering dmsg servers...
[2022-05-26T18:25:53-05:00] INFO [dmsg]: Initialized in 24.737µs (26.254µs with dependencies)
[2022-05-26T18:25:53-05:00] INFO [address_resolver]: Remote UDP server: "ar.skywire.skycoin.com:30178"
[2022-05-26T18:25:53-05:00] INFO [address_resolver]: Initialized in 75.152µs (76.353µs with dependencies)
[2022-05-26T18:25:53-05:00] INFO [hypervisors]: Initialized in 1.096µs (283.069µs with dependencies)
[2022-05-26T18:25:53-05:00] INFO [dmsg_pty]: Initialized in 125.52µs (276.903µs with dependencies)
[2022-05-26T18:25:53-05:00] INFO [cli]: Initialized in 1.798078ms (1.798324ms with dependencies)
[2022-05-26T18:25:54-05:00] INFO [address_resolver]: Connected to address resolver. STCPR/SUDPH services are available.
[2022-05-26T18:25:54-05:00] INFO [uptime_tracker]: Initialized in 511.911439ms (512.145993ms with dependencies)
[2022-05-26T18:25:54-05:00] INFO [dmsgC]: Dialing session... remote_pk=0371ab4bcff7b121f4b91f6856d6740c6f9dc1fe716977850aeb5d84378b300a13
[2022-05-26T18:25:54-05:00] INFO [transport]: Initialized in 551.607332ms (551.811125ms with dependencies)
[2022-05-26T18:25:54-05:00] INFO [transport_manager]: transport manager is serving.
[2022-05-26T18:25:54-05:00] INFO [transport_manager]: Serving stcp network
[2022-05-26T18:25:54-05:00] INFO [transport_setup]: Connecting to the dmsg network. local_pk=022769f159bb6f67c13140a214d5da0f2192d0881f00449460c458e17eb60115a7
[2022-05-26T18:25:54-05:00] INFO [transport_manager]: listening on network: stcp
[2022-05-26T18:25:54-05:00] INFO [transport_manager]: Serving stcpr network
[2022-05-26T18:25:54-05:00] INFO [stcp]: Initialized in 155.253µs (552.193261ms with dependencies)
[2022-05-26T18:25:54-05:00] INFO [dmsgC]: Connecting to the dmsg network... timeout=20s
[2022-05-26T18:25:54-05:00] INFO [transport_manager]: listening on network: stcpr
[2022-05-26T18:25:54-05:00] INFO [transport_setup]: Initialized in 278.717µs (552.54836ms with dependencies)
[2022-05-26T18:25:54-05:00] INFO [router]: Starting router
[2022-05-26T18:25:54-05:00] INFO [stcpr]: Initialized in 111.115µs (552.401847ms with dependencies)
[2022-05-26T18:25:54-05:00] INFO [router]: Initialized in 323.274µs (552.54424ms with dependencies)
[2022-05-26T18:25:54-05:00] INFO [stcp]: listening on addr: [::]:7777
[2022-05-26T18:25:54-05:00] INFO [public_autoconnect]: Initialized in 19.849µs (552.484224ms with dependencies)
[2022-05-26T18:25:54-05:00] INFO [stcpr]: Not binding STCPR: no public IP address found
[2022-05-26T18:25:54-05:00] INFO [launcher]: Initialized in 8.727881ms (561.321685ms with dependencies)
[2022-05-26T18:25:54-05:00] INFO [proc_manager]: Accepted proc conn. hello="{"proc_key":"f15a3cc2dd444f6080b9e2aee51834dc"}" remote=127.0.0.1:41358
[2022-05-26T18:25:54-05:00] INFO (STDOUT) [proc:skysocks:567eb90e0d58459582636d996bcc40ea]: Version "unknown" built on "unknown" against commit "unknown"
[2022-05-26T18:25:54-05:00] INFO [proc_manager]: Accepted proc conn. hello="{"proc_key":"567eb90e0d58459582636d996bcc40ea"}" remote=127.0.0.1:41354
[2022-05-26T18:25:54-05:00] INFO (STDOUT) [proc:skychat:f15a3cc2dd444f6080b9e2aee51834dc]: Version "unknown" built on "unknown" against commit "unknown"
[2022-05-26T18:25:54-05:00] INFO [proc:skysocks:567eb90e0d58459582636d996bcc40ea]: Associated and serving proc conn.
[2022-05-26T18:25:54-05:00] INFO [proc:skychat:f15a3cc2dd444f6080b9e2aee51834dc]: Associated and serving proc conn.
[2022-05-26T18:25:54-05:00] INFO [proc:skychat:f15a3cc2dd444f6080b9e2aee51834dc]: Request processed. _elapsed="7.047µs" _method="SetDetailedStatus" _received="6:25PM" input=0xc0003643d0
[2022-05-26T18:25:54-05:00] INFO [proc:skychat:f15a3cc2dd444f6080b9e2aee51834dc]: Request processed. _elapsed="1.160541ms" _method="Listen" _received="6:25PM" input=022769f159bb6f67c13140a214d5da0f2192d0881f00449460c458e17eb60115a7:1 output=0xc0003568f6
[2022-05-26T18:25:54-05:00] INFO [proc:skysocks:567eb90e0d58459582636d996bcc40ea]: Request processed. _elapsed="2.955995ms" _method="Listen" _received="6:25PM" input=022769f159bb6f67c13140a214d5da0f2192d0881f00449460c458e17eb60115a7:3 output=0xc0001f27ce
[2022-05-26T18:25:54-05:00] INFO (STDOUT) [proc:skychat:f15a3cc2dd444f6080b9e2aee51834dc]: Successfully started skychat.Serving HTTP on:8001Accepting skychat conn...Calling app RPC Accept
[2022-05-26T18:25:54-05:00] INFO (STDOUT) [proc:skysocks:567eb90e0d58459582636d996bcc40ea]: Starting serving proxy server
[2022-05-26T18:25:54-05:00] INFO [proc:skysocks:567eb90e0d58459582636d996bcc40ea]: Request processed. _elapsed="9.089µs" _method="SetDetailedStatus" _received="6:25PM" input=0xc0003640b0
[2022-05-26T18:25:54-05:00] INFO (STDOUT) [proc:skysocks:567eb90e0d58459582636d996bcc40ea]: Calling app RPC Accept
[2022-05-26T18:25:54-05:00] INFO [public_visor]: Initialized in 706.738717ms (1.259188636s with dependencies)
[2022-05-26T18:25:54-05:00] INFO [visor]: Initialized in 233ns (1.264249204s with dependencies)
[2022-05-26T18:25:54-05:00] INFO [visor]: Initializing hypervisor
[2022-05-26T18:25:54-05:00] INFO [visor]: Serving RPC client over dmsg. addr=022769f159bb6f67c13140a214d5da0f2192d0881f00449460c458e17eb60115a7:46
[2022-05-26T18:25:54-05:00] INFO [visor]: Serving hypervisor... addr=":8000" tls=false
[2022-05-26T18:25:54-05:00] INFO [visor]: Hypervisor initialized
[2022-05-26T18:25:54-05:00] INFO [hypervisor]: Initialized in 7.763556ms (1.273603766s with dependencies)
[2022-05-26T18:25:55-05:00] INFO [dmsgC]: Serving session. remote_pk=0371ab4bcff7b121f4b91f6856d6740c6f9dc1fe716977850aeb5d84378b300a13
[2022-05-26T18:25:55-05:00] INFO [dmsgC]: Connected to the dmsg network. timeout=20s
[2022-05-26T18:25:55-05:00] INFO [transport_setup]: Connected! local_pk=022769f159bb6f67c13140a214d5da0f2192d0881f00449460c458e17eb60115a7
[2022-05-26T18:25:55-05:00] INFO [transport_manager]: Serving dmsg network
[2022-05-26T18:25:55-05:00] INFO [transport_setup]: starting listener dmsg_port=47
[2022-05-26T18:25:55-05:00] INFO [transport_manager]: listening on network: dmsg
[2022-05-26T18:25:55-05:00] INFO [transport_setup]: Accepting dmsg streams. dmsg_port=47
[2022-05-26T18:25:55-05:00] INFO [dmsg_ctrl]: Initialized in 906.882851ms (1.459256725s with dependencies)
^C[2022-05-26T18:25:55-05:00] INFO []: Closing with received signal. signal=interrupt
[2022-05-26T18:25:55-05:00] INFO [transport_setup]: Dmsg client stopped serving. error="dmsg error 200 - local entity closed"
[2022-05-26T18:25:55-05:00] INFO [visor:shutdown]: Begin shutdown.
[2022-05-26T18:25:55-05:00] INFO [proc:skysocks:567eb90e0d58459582636d996bcc40ea]: Request processed. _elapsed="15.322µs" _method="CloseListener" _received="6:25PM" input=0xc00040ccbc
[2022-05-26T18:25:55-05:00] INFO [proc:skysocks:567eb90e0d58459582636d996bcc40ea]: Request processed. _elapsed="1.235717572s" _method="Accept" _received="6:25PM" error="listening on closed connection" input=0xc00040c7e4 output=&{Remote:000000000000000000000000000000000000000000000000000000000000000000:~ ConnID:0}
[2022-05-26T18:25:55-05:00] INFO [proc:skychat:f15a3cc2dd444f6080b9e2aee51834dc]: Request processed. _elapsed="1.241078015s" _method="Accept" _received="6:25PM" error="listening on closed connection" input=0xc000356050 output=&{Remote:000000000000000000000000000000000000000000000000000000000000000000:~ ConnID:0}
[2022-05-26T18:25:55-05:00] INFO [12/12] [visor:shutdown:dmsgctrl]: Shutting down module...
[2022-05-26T18:25:55-05:00] INFO [12/12] [visor:shutdown:dmsgctrl]: Module stopped cleanly. elapsed=4.885856ms
[2022-05-26T18:25:55-05:00] INFO [11/12] [visor:shutdown:launcher.proc_manager]: Shutting down module...
[2022-05-26T18:25:55-05:00] INFO [proc_manager]: App stopped successfully. app_name="skysocks"
[2022-05-26T18:25:55-05:00] INFO [11/12] [visor:shutdown:launcher.proc_manager]: Module stopped cleanly. elapsed=2.799901ms
[2022-05-26T18:25:55-05:00] INFO [10/12] [visor:shutdown:router.serve]: Shutting down module...
[2022-05-26T18:25:55-05:00] INFO [router]: Closing all App connections and RouteGroups
[2022-05-26T18:25:55-05:00] INFO [10/12] [visor:shutdown:router.serve]: Module stopped cleanly. elapsed=2.635927ms
[2022-05-26T18:25:55-05:00] INFO serveRouteGroup [launcher]: Stopped accepting routes. _=skynet error="accept skynet: use of closed network connection"
[2022-05-26T18:25:55-05:00] INFO [router]: Setup client stopped serving. error="dmsg error 200 - local entity closed"
[2022-05-26T18:25:55-05:00] INFO [9/12] [visor:shutdown:transport_setup.rpc]: Shutting down module...
[2022-05-26T18:25:55-05:00] INFO [9/12] [visor:shutdown:transport_setup.rpc]: Module stopped cleanly. elapsed=3.019114ms
[2022-05-26T18:25:55-05:00] INFO [8/12] [visor:shutdown:transport.manager]: Shutting down module...
[2022-05-26T18:25:55-05:00] INFO [transport_manager]: transport manager is closing.
[2022-05-26T18:25:55-05:00] INFO [stcp]: Cleanly stopped serving.
[2022-05-26T18:25:55-05:00] INFO [transport_manager]: transport manager closed.
[2022-05-26T18:25:55-05:00] INFO [router]: Stopped reading packets error="transport is no longer being served"
[2022-05-26T18:25:55-05:00] INFO [8/12] [visor:shutdown:transport.manager]: Module stopped cleanly. elapsed=4.785588ms
[2022-05-26T18:25:55-05:00] INFO [7/12] [visor:shutdown:uptime_tracker]: Shutting down module...
[2022-05-26T18:25:55-05:00] INFO [7/12] [visor:shutdown:uptime_tracker]: Module stopped cleanly. elapsed=1.225732ms
[2022-05-26T18:25:55-05:00] INFO [6/12] [visor:shutdown:cli.listener]: Shutting down module...
2022/05/26 18:25:55 rpc.Serve: accept:accept tcp 127.0.0.1:3435: use of closed network connection
[2022-05-26T18:25:55-05:00] INFO [6/12] [visor:shutdown:cli.listener]: Module stopped cleanly. elapsed=1.357598ms
[2022-05-26T18:25:55-05:00] INFO [5/12] [visor:shutdown:router.serve]: Shutting down module...
[2022-05-26T18:25:55-05:00] INFO [dmsg_pty:cli-server]: Cleanly stopped serving.
[2022-05-26T18:25:55-05:00] INFO [5/12] [visor:shutdown:router.serve]: Module stopped cleanly. elapsed=3.209776ms
[2022-05-26T18:25:55-05:00] INFO [4/12] [visor:shutdown:address_resolver]: Shutting down module...
[2022-05-26T18:25:55-05:00] INFO [4/12] [visor:shutdown:address_resolver]: Module stopped cleanly. elapsed=1.307808ms
[2022-05-26T18:25:55-05:00] INFO [3/12] [visor:shutdown:router.serve]: Shutting down module...
[2022-05-26T18:25:55-05:00] INFO [dmsg_pty]: Serve() ended. error=<nil>
[2022-05-26T18:25:55-05:00] INFO [dmsg_pty]: Cleanly stopped serving. error="dmsg error 200 - local entity closed"
[2022-05-26T18:25:55-05:00] INFO [3/12] [visor:shutdown:router.serve]: Module stopped cleanly. elapsed=3.578724ms
[2022-05-26T18:25:55-05:00] INFO [2/12] [visor:shutdown:dmsg]: Shutting down module...
[2022-05-26T18:25:55-05:00] INFO [dmsgC]: Stopped accepting streams. error="session shutdown" session=0371ab4bcff7b121f4b91f6856d6740c6f9dc1fe716977850aeb5d84378b300a13
[2022-05-26T18:25:55-05:00] INFO [dmsgC]: Stopped serving client!
[2022-05-26T18:25:55-05:00] INFO [dmsgC]: Session closed. error=<nil>
[2022-05-26T18:25:55-05:00] INFO [dmsgC]: All sessions closed.
[2022-05-26T18:25:55-05:00] INFO [transport_manager]: Dmsg client stopped serving. error="dmsg error 200 - local entity closed"
[2022-05-26T18:25:55-05:00] INFO [dmsgC]: Dialing session... remote_pk=0371ab4bcff7b121f4b91f6856d6740c6f9dc1fe716977850aeb5d84378b300a13
[2022-05-26T18:25:56-05:00] INFO [2/12] [visor:shutdown:dmsg]: Module stopped cleanly. elapsed=662.819011ms
[2022-05-26T18:25:56-05:00] INFO [1/12] [visor:shutdown:event_broadcaster]: Shutting down module...
[2022-05-26T18:25:56-05:00] INFO [1/12] [visor:shutdown:event_broadcaster]: Module stopped cleanly. elapsed=1.468749ms
[2022-05-26T18:25:56-05:00] INFO [visor:shutdown]: Shutdown complete. Goodbye!
[2022-05-26T18:25:56-05:00] INFO []: Visor closed early.

```
