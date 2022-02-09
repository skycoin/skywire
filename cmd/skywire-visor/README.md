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
$ go run ./cmd/skywire-visor/skywire-visor.go --help
Skywire visor

Usage:
  skywire-visor [flags]
  skywire-visor [command]

Available Commands:
  completion  Generate completion script
  help        Help about any command

Flags:
  -c, --config string        config file location. If the value is 'STDIN', config file will be read from stdin.
      --delay string         start delay (deprecated) (default "0ns")
  -h, --help                 help for skywire-visor
      --launch-browser       open hypervisor web ui (hypervisor only) with system browser
      --pprofaddr string     pprof http port if mode is 'http' (default "localhost:6060")
  -p, --pprofmode string     pprof profiling mode. Valid values: cpu, mem, mutex, block, trace, http
      --syslog string        syslog server address. E.g. localhost:514
      --tag string           logging tag (default "skywire")
  -v, --version              version for skywire-visor
  -f, --with-hypervisor-ui   run visor with hypervisor UI config.

Use "skywire-visor [command] --help" for more information about a command.

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

the following runs skywire from source without compiling:

```
$ ln -s scripts/_apps apps
$ chmod +x apps/*
$ go run ./cmd/skywire-cli/skywire-cli.go config gen -ibro ./skywire-config.json
$ go run ./cmd/skywire-visor/skywire-visor.go -c ./skywire-config.json
```

Or, shorthand:

```
make run-source
```


##### Example running from source dir

The default filename and paths in the skywire-config.json file are designed for the context of running skywire-visor from within the cloned source repository, wherever it may reside. A brief example of the terminal ouput when running skywire-visor where skywire-config.json exists in the current directory:

```
$ skywire-visor -c skywire-config.json
[2022-02-07T18:49:00-06:00] DEBUG []: Process info build_tag="" delay=0s parent_systemd=false skybian_build_version="" systemd=false
Version "unknown" built on "unknown" against commit "unknown"
[2022-02-07T18:49:00-06:00] INFO [visor:config]: Reading config from file. filepath="skywire-config.json"
[2022-02-07T18:49:00-06:00] INFO [visor:config]: Flushing config to file. config_version="v1.1.0" filepath="skywire-config.json"
[2022-02-07T18:49:00-06:00] INFO [visor:startup]: Begin startup. public_key=02da35804dd25dbb91e64e64ca43336206538a775054702d2e543299e5168660ef
[2022-02-07T18:49:00-06:00] INFO [hypervisor]: Starting
[2022-02-07T18:49:00-06:00] INFO [visor]: Starting
[2022-02-07T18:49:00-06:00] INFO [transports]: Starting
[2022-02-07T18:49:00-06:00] INFO [router]: Starting
[2022-02-07T18:49:00-06:00] INFO [cli]: Starting
[2022-02-07T18:49:00-06:00] INFO [updater]: Starting
[2022-02-07T18:49:00-06:00] INFO [dmsg_ctrl]: Starting
[2022-02-07T18:49:00-06:00] INFO [stcpr]: Starting
[2022-02-07T18:49:00-06:00] INFO [public_visor]: Starting
[2022-02-07T18:49:00-06:00] INFO [public_autoconnect]: Starting
[2022-02-07T18:49:00-06:00] INFO [transport_setup]: Starting
[2022-02-07T18:49:00-06:00] INFO [discovery]: Starting
[2022-02-07T18:49:00-06:00] INFO [stcp]: Starting
[2022-02-07T18:49:00-06:00] INFO [address_resolver]: Starting
[2022-02-07T18:49:00-06:00] INFO [dmsg_pty]: Starting
[2022-02-07T18:49:00-06:00] INFO [hypervisors]: Starting
[2022-02-07T18:49:00-06:00] INFO [uptime_tracker]: Starting
[2022-02-07T18:49:00-06:00] INFO [transport]: Starting
[2022-02-07T18:49:00-06:00] INFO [launcher]: Starting
[2022-02-07T18:49:00-06:00] INFO [event_broadcaster]: Starting
[2022-02-07T18:49:00-06:00] INFO [event_broadcaster]: Initialized in 2.929µs (3.236µs with dependencies)
[2022-02-07T18:49:00-06:00] INFO [stun_client]: Starting
[2022-02-07T18:49:00-06:00] INFO [sudph]: Starting
[2022-02-07T18:49:00-06:00] INFO [dmsg_http]: Starting
[2022-02-07T18:49:00-06:00] INFO [dmsg_http]: Initialized in 551ns (797ns with dependencies)
[2022-02-07T18:49:00-06:00] INFO [dmsg]: Starting
[2022-02-07T18:49:00-06:00] INFO [updater]: Initialized in 1.984µs (2.375µs with dependencies)
[2022-02-07T18:49:00-06:00] INFO [dmsgC]: Discovering dmsg servers...
[2022-02-07T18:49:00-06:00] INFO [discovery]: Initialized in 33.548µs (713.928µs with dependencies)
[2022-02-07T18:49:00-06:00] INFO [address_resolver]: Remote UDP server: "ar.skywire.skycoin.com:30178"
[2022-02-07T18:49:00-06:00] INFO [dmsg]: Initialized in 62.689µs (66.823µs with dependencies)
[2022-02-07T18:49:00-06:00] INFO [hypervisors]: Initialized in 989ns (762.642µs with dependencies)
[2022-02-07T18:49:00-06:00] INFO [dmsg_pty]: Initialized in 158.188µs (1.009816ms with dependencies)
[2022-02-07T18:49:00-06:00] INFO [cli]: Initialized in 1.829639ms (1.829877ms with dependencies)
[2022-02-07T18:49:01-06:00] INFO [uptime_tracker]: Initialized in 432.529335ms (432.952838ms with dependencies)
[2022-02-07T18:49:01-06:00] INFO [dmsgC]: Dialing session... remote_pk=02347729662a901d03f1a1ab6c189a173349fa11e79fe82117cca0f8d0e4d64a31
[2022-02-07T18:49:01-06:00] INFO [address_resolver]: Connected to address resolver. STCPR/SUDPH services are available.
[2022-02-07T18:49:01-06:00] INFO [address_resolver]: Initialized in 987.461316ms (988.050386ms with dependencies)
[2022-02-07T18:49:02-06:00] INFO [dmsgC]: Serving session. remote_pk=02347729662a901d03f1a1ab6c189a173349fa11e79fe82117cca0f8d0e4d64a31
[2022-02-07T18:49:02-06:00] INFO [transport]: Initialized in 470.128967ms (1.458111681s with dependencies)
[2022-02-07T18:49:02-06:00] INFO [dmsgC]: Connecting to the dmsg network... timeout=20s
[2022-02-07T18:49:02-06:00] INFO [transport_manager]: transport manager is serving.
[2022-02-07T18:49:02-06:00] INFO [transport_setup]: Connecting to the dmsg network. local_pk=02da35804dd25dbb91e64e64ca43336206538a775054702d2e543299e5168660ef
[2022-02-07T18:49:02-06:00] INFO [transport_setup]: Connected! local_pk=02da35804dd25dbb91e64e64ca43336206538a775054702d2e543299e5168660ef
[2022-02-07T18:49:02-06:00] INFO [transport_setup]: starting listener dmsg_port=47
[2022-02-07T18:49:02-06:00] INFO [transport_setup]: Accepting dmsg streams. dmsg_port=47
[2022-02-07T18:49:02-06:00] INFO [transport_manager]: Serving stcp network
[2022-02-07T18:49:02-06:00] INFO [transport_manager]: listening on network: stcp
[2022-02-07T18:49:02-06:00] INFO [stcp]: Initialized in 366.457µs (1.458857374s with dependencies)
[2022-02-07T18:49:02-06:00] INFO [transport_setup]: Initialized in 361.602µs (1.458985676s with dependencies)
[2022-02-07T18:49:02-06:00] INFO [transport_manager]: Serving stcpr network
[2022-02-07T18:49:02-06:00] INFO [transport_manager]: listening on network: stcpr
[2022-02-07T18:49:02-06:00] INFO [stcp]: listening on addr: [::]:7777
[2022-02-07T18:49:02-06:00] INFO [public_autoconnect]: Initialized in 9.502µs (1.458628811s with dependencies)
[2022-02-07T18:49:02-06:00] INFO [router]: Starting router
[2022-02-07T18:49:02-06:00] INFO [dmsgC]: Connected to the dmsg network. timeout=20s
[2022-02-07T18:49:02-06:00] INFO [transport_manager]: Serving dmsg network
[2022-02-07T18:49:02-06:00] INFO [transport_manager]: listening on network: dmsg
[2022-02-07T18:49:02-06:00] INFO [stcpr]: Not binding STCPR: no public IP address found
[2022-02-07T18:49:02-06:00] INFO [dmsg_ctrl]: Initialized in 696.857µs (1.459423545s with dependencies)
[2022-02-07T18:49:02-06:00] INFO [router]: Initialized in 642.448µs (1.459665206s with dependencies)
[2022-02-07T18:49:02-06:00] INFO [stcpr]: Initialized in 447.316µs (1.459153332s with dependencies)
[2022-02-07T18:49:02-06:00] INFO [public_visor]: Initialized in 787ns (1.459423411s with dependencies)
[2022-02-07T18:49:02-06:00] INFO [launcher]: Initialized in 21.113327ms (1.480061107s with dependencies)
[2022-02-07T18:49:02-06:00] INFO [visor]: Initialized in 226ns (1.502320595s with dependencies)
[2022-02-07T18:49:02-06:00] INFO [visor]: Initializing hypervisor
[2022-02-07T18:49:02-06:00] INFO [visor]: Serving RPC client over dmsg. addr=02da35804dd25dbb91e64e64ca43336206538a775054702d2e543299e5168660ef:46
[2022-02-07T18:49:02-06:00] INFO [visor]: Serving hypervisor... addr=":8000" tls=false
[2022-02-07T18:49:02-06:00] INFO [visor]: Hypervisor initialized
[2022-02-07T18:49:02-06:00] INFO [hypervisor]: Initialized in 19.833078ms (1.526147062s with dependencies)
```

##### Example for package based installation

Assuming that skywire is installed to /opt/skywire; with app binaries installed at /opt/skywire/apps and the main skywire binaries directly in or symlinked to the executable path. All files generated by running skywire-visor should populate in /opt/skywire

The configuration file is generated in the following way

for a visor with local hypervisor:

```
$   skywire-cli config gen -bipr --enable-auth -o /opt/skywire/skywire.json
```

for visor with remote hypervisor; first copy the existing configuration file to keep the same keys.

```
# cp /opt/skywire/skywire.json /opt/skywire/skywire-visor.json
# skywire-cli config gen --hypervisor-pks <remote-hypervisor-public-key> -bpo /opt/skywire/skywire-visor.json
```

These two configuration files are referenced in systemd service files to start skywire with either a local or remote hypervisor.

To clarify some terminology; a 'visor' is a running instance of skywire-visor. Previously, on the testnet, this was called a 'node'. This was changed for the sake of differentiating the hardware from the software when troubleshooting issues. The 'public key' in the configuration file is referred to as the 'visor key' or the visor's public key. In the context of using a visor which is running a hypervisor instance as a remote hypervisor for another visor, the public key (visor key) of the visor running the hypervisor web instance is referred to as the 'hypervisor key' or the hypervisor's public key. A hypervisor key is a visor key, these terms are used interchangeably and refer to the same thing for a visor running a hypervisor instance.

```
# skywire -c /opt/skywire/skywire.json
[2022-02-07T18:54:55-06:00] DEBUG []: Process info build_tag="linux_amd64" delay=0s parent_systemd=false skybian_build_version="" systemd=false
Version "v0.6.0-rc1" built on "2022-02-04T13:58:58Z" against commit "74fde018"
[2022-02-07T18:54:55-06:00] INFO [visor:config]: Reading config from file. filepath="/opt/skywire/skywire.json"
[2022-02-07T18:54:55-06:00] INFO [visor:config]: Flushing config to file. config_version="v1.1.0" filepath="/opt/skywire/skywire.json"
[2022-02-07T18:54:55-06:00] INFO [visor:startup]: Begin startup. public_key=027087fe40d97f7f0be4a0dc768462ddbb371d4b9e7679d4f11f117d757b9856ed
[2022-02-07T18:54:55-06:00] INFO [hypervisor]: Starting
[2022-02-07T18:54:55-06:00] INFO [visor]: Starting
[2022-02-07T18:54:55-06:00] INFO [stcpr]: Starting
[2022-02-07T18:54:55-06:00] INFO [transport]: Starting
[2022-02-07T18:54:55-06:00] INFO [launcher]: Starting
[2022-02-07T18:54:55-06:00] INFO [updater]: Starting
[2022-02-07T18:54:55-06:00] INFO [public_visor]: Starting
[2022-02-07T18:54:55-06:00] INFO [stcp]: Starting
[2022-02-07T18:54:55-06:00] INFO [address_resolver]: Starting
[2022-02-07T18:54:55-06:00] INFO [updater]: Initialized in 3.209µs (3.578µs with dependencies)
[2022-02-07T18:54:55-06:00] INFO [router]: Starting
[2022-02-07T18:54:55-06:00] INFO [dmsg_http]: Starting
[2022-02-07T18:54:55-06:00] INFO [dmsg_http]: Initialized in 720ns (981ns with dependencies)
[2022-02-07T18:54:55-06:00] INFO [discovery]: Starting
[2022-02-07T18:54:55-06:00] INFO [discovery]: Initialized in 13.829µs (15.098µs with dependencies)
[2022-02-07T18:54:55-06:00] INFO [dmsg_pty]: Starting
[2022-02-07T18:54:55-06:00] INFO [uptime_tracker]: Starting
[2022-02-07T18:54:55-06:00] INFO [transports]: Starting
[2022-02-07T18:54:55-06:00] INFO [dmsg_ctrl]: Starting
[2022-02-07T18:54:55-06:00] INFO [event_broadcaster]: Starting
[2022-02-07T18:54:55-06:00] INFO [sudph]: Starting
[2022-02-07T18:54:55-06:00] INFO [dmsg]: Starting
[2022-02-07T18:54:55-06:00] INFO [transport_setup]: Starting
[2022-02-07T18:54:55-06:00] INFO [public_autoconnect]: Starting
[2022-02-07T18:54:55-06:00] INFO [cli]: Starting
[2022-02-07T18:54:55-06:00] INFO [address_resolver]: Remote UDP server: "ar.skywire.skycoin.com:30178"
[2022-02-07T18:54:55-06:00] INFO [stun_client]: Starting
[2022-02-07T18:54:55-06:00] INFO [hypervisors]: Starting
[2022-02-07T18:54:55-06:00] INFO [event_broadcaster]: Initialized in 2.329µs (2.548µs with dependencies)
[2022-02-07T18:54:55-06:00] INFO [dmsg]: Initialized in 22.794µs (367.528µs with dependencies)
[2022-02-07T18:54:55-06:00] INFO [hypervisors]: Initialized in 915ns (85.737µs with dependencies)
[2022-02-07T18:54:55-06:00] INFO [dmsgC]: Discovering dmsg servers...
[2022-02-07T18:54:55-06:00] INFO [dmsg_pty]: Initialized in 54.266µs (656.963µs with dependencies)
[2022-02-07T18:54:55-06:00] INFO [cli]: Initialized in 404.237µs (404.515µs with dependencies)
[2022-02-07T18:54:55-06:00] INFO [dmsgC]: Dialing session... remote_pk=02347729662a901d03f1a1ab6c189a173349fa11e79fe82117cca0f8d0e4d64a31
[2022-02-07T18:54:55-06:00] INFO [uptime_tracker]: Initialized in 509.132965ms (509.13433ms with dependencies)
[2022-02-07T18:54:55-06:00] INFO [address_resolver]: Connected to address resolver. STCPR/SUDPH services are available.
[2022-02-07T18:54:56-06:00] INFO [address_resolver]: Initialized in 1.03416369s (1.034390128s with dependencies)
[2022-02-07T18:54:56-06:00] INFO [dmsgC]: Serving session. remote_pk=02347729662a901d03f1a1ab6c189a173349fa11e79fe82117cca0f8d0e4d64a31
[2022-02-07T18:54:56-06:00] INFO [transport]: Initialized in 507.868374ms (1.542672709s with dependencies)
[2022-02-07T18:54:56-06:00] INFO [transport_manager]: Serving stcpr network
[2022-02-07T18:54:56-06:00] INFO [transport_manager]: transport manager is serving.
[2022-02-07T18:54:56-06:00] INFO [transport_setup]: Connecting to the dmsg network. local_pk=027087fe40d97f7f0be4a0dc768462ddbb371d4b9e7679d4f11f117d757b9856ed
[2022-02-07T18:54:56-06:00] INFO [transport_setup]: Connected! local_pk=027087fe40d97f7f0be4a0dc768462ddbb371d4b9e7679d4f11f117d757b9856ed
[2022-02-07T18:54:56-06:00] INFO [transport_manager]: listening on network: stcpr
[2022-02-07T18:54:56-06:00] INFO [transport_setup]: starting listener dmsg_port=47
[2022-02-07T18:54:56-06:00] INFO [stcpr]: Initialized in 471.448µs (1.543308565s with dependencies)
[2022-02-07T18:54:56-06:00] INFO [transport_setup]: Accepting dmsg streams. dmsg_port=47
[2022-02-07T18:54:56-06:00] INFO [stcpr]: Not binding STCPR: no public IP address found
[2022-02-07T18:54:56-06:00] INFO [transport_manager]: Serving stcp network
[2022-02-07T18:54:56-06:00] INFO [transport_manager]: listening on network: stcp
[2022-02-07T18:54:56-06:00] INFO [transport_setup]: Initialized in 481.348µs (1.542390746s with dependencies)
[2022-02-07T18:54:56-06:00] INFO [public_visor]: Initialized in 641ns (1.543038283s with dependencies)
[2022-02-07T18:54:56-06:00] INFO [dmsgC]: Connecting to the dmsg network... timeout=20s
[2022-02-07T18:54:56-06:00] INFO [dmsgC]: Connected to the dmsg network. timeout=20s
[2022-02-07T18:54:56-06:00] INFO [transport_manager]: Serving dmsg network
[2022-02-07T18:54:56-06:00] INFO [public_autoconnect]: Initialized in 67.16µs (1.542018866s with dependencies)
[2022-02-07T18:54:56-06:00] INFO [router]: Starting router
[2022-02-07T18:54:56-06:00] INFO [router]: Initialized in 926.371µs (1.543240321s with dependencies)
[2022-02-07T18:54:56-06:00] INFO [stcp]: Initialized in 590.35µs (1.54314418s with dependencies)
[2022-02-07T18:54:56-06:00] INFO [stcp]: listening on addr: [::]:7777
[2022-02-07T18:54:56-06:00] INFO [transport_manager]: listening on network: dmsg
[2022-02-07T18:54:56-06:00] INFO [dmsg_ctrl]: Initialized in 1.011469ms (1.543159903s with dependencies)
[2022-02-07T18:54:56-06:00] INFO [launcher]: Initialized in 12.681013ms (1.556420507s with dependencies)
[2022-02-07T18:54:56-06:00] INFO [visor]: Initialized in 245ns (1.566227368s with dependencies)
[2022-02-07T18:54:56-06:00] INFO [visor]: Initializing hypervisor
[2022-02-07T18:54:56-06:00] INFO [visor]: Serving RPC client over dmsg. addr=027087fe40d97f7f0be4a0dc768462ddbb371d4b9e7679d4f11f117d757b9856ed:46
[2022-02-07T18:54:56-06:00] INFO [visor]: Serving hypervisor... addr=":8000" tls=false
[2022-02-07T18:54:56-06:00] INFO [visor]: Hypervisor initialized
[2022-02-07T18:54:56-06:00] INFO [hypervisor]: Initialized in 42.392586ms (1.616936619s with dependencies)
```
