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
```
$ skywire-visor --help


┌─┐┬┌─┬ ┬┬ ┬┬┬─┐┌─┐
└─┐├┴┐└┬┘││││├┬┘├┤
└─┘┴ ┴ ┴ └┴┘┴┴└─└─┘

Flags:
-c, --config string   config file to use (default): skywire-config.json
		--systray         run as systray
-i, --hvui            run as hypervisor *
		--all             show all flags
-h, --help            help for visor
-v, --version         version for visor



$ skywire-visor --all

		┌─┐┬┌─┬ ┬┬ ┬┬┬─┐┌─┐
		└─┐├┴┐└┬┘││││├┬┘├┤
		└─┘┴ ┴ ┴ └┴┘┴┴└─└─┘

	 Flags:
	  -c, --config string        config file to use (default): skywire-config.json
	      --dmsg-server string   use specified dmsg server public key
	  -n, --stdin                read config from stdin
	  -p, --pkg                  use package config /opt/skywire/skywire.json
	  -u, --user                 u̶s̶e̶r̶s̶p̶a̶c̶e̶ ̶c̶o̶n̶f̶i̶g̶ does not exist
	      --systray              run as systray
	  -i, --hvui                 run as hypervisor *
	  -x, --nohvui               disable hypervisor *
	  -j, --hv string            add remote hypervisor *
	  -k, --xhv                  disable remote hypervisors *
	  -s, --loglvl string        [ debug | warn | error | fatal | panic | trace ] *
	  -q, --pprofmode string     [ cpu | mem | mutex | block | trace | http ]
	  -r, --pprofaddr string     pprof http port (default "localhost:6060")
	  -t, --logtag string        logging tag (default "skywire")
	  -y, --syslog string        syslog server address. E.g. localhost:514
	  -z, --completion string    [ bash | zsh | fish | powershell ]
	  -l, --storelog             store all logs to file
	      --forcecolor           force color logging when out is not STDOUT
	  -v, --version              version for visor
	                            * overrides config file
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

## Config file generation

Refer to the [skywire-cli documentation](../skywire-cli/README.md) for more detailed information regarding additional flags and argument that may be passed to the following command:

```
skywire-cli config gen
```

With no additional flags or arguments, the configuration is written to skywire-config.json and stdout along with logging to stdout.
