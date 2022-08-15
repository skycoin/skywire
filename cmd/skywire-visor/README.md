# Skywire Visor Documentation

<!-- MarkdownTOC levels="1,2,3,4,5" autolink="true" bracket="round" -->
- [Install](#install)
- [skywire-visor usage](#skywire-visor-usage)
	- [flags](#skywire-visor-flags)
- [config file generation](#config-file-generation)

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

After the installation, you can run `skywire-visor -h`  to see the usage or `skywire-visor --all` for advanced usage:
_Note: flags for autopeering are only available with the environmental variable `SKYBIAN=true`
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
    -b, --browser             open hypervisor ui in default web browser
    -i, --hvui                run as hypervisor
    -j, --hv string           add remote hypervisor PKs at runtime
    -k, --xhv                 disable remote hypervisors set in config file
    -l, --hvip string         set hypervisor by ip (default "192.168.2.2:7998")
    -m, --autopeer            enable autopeering
    -n, --stdin               read config from stdin
    -q, --pprofmode string    pprof mode: cpu, mem, mutex, block, trace, http
    -r, --pprofaddr string    pprof http port (default "localhost:6060")
    -t, --tag string          logging tag (default "skywire")
    -y, --syslog string       syslog server address. E.g. localhost:514
    -z, --completion string   [ bash | zsh | fish | powershell ]
    -h, --help                help for skywire-visor
    -v, --version             version for skywire-visor
```

### Skywire visor flags

Mutually exclusive flags:

* `    -n, --stdin`
* `    -c, --config`
* `    -p, --pkg`
	- requires sudo / root permissions
	- only shown when the config file exists
* `    -u, --user`
	- requires user permissions
	- only shown when the config file exists

The `    -b, --browser` flag is not available to root / with sudo.

### Autopeering visors to a hypervisor

The autopeering system is used in skybian to peer visors to a remote hypervisor on a predetermined static IP address.

For the autopeering of visors to a remote hypervisor to work, neither a remote nor a local hypervisor may be set in the config file.

The `-m` and `-l` flags will simply be ignored if there are remote or local hypervisors in the config file used by the visor

As well, the environmental variable `SKYBIAN=true` must be present

```
skywire-visor -mp
```

To use a desktop as the hypervisor which visors autopeer to, run the `skywire-cli visor pk -w` command on the desktop and specify on the visors (or in the visor's systemd service file) the ip address of the desired hypervisor using the `-l` flag for skywire-visor

## Config file generation

Refer to the [skywire-cli documentation](../skywire-cli/README.md) for more detailed information regarding additional flags and argument that may be passed to the following command:

```
skywire-cli config gen
```

With no additional flags or arguments, the configuration is written to skywire-config.json and stdout.
