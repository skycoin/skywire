# CLI Documentation

skywire command line interface

<!-- MarkdownTOC autolink="true" bracket="round" levels="1,2,3" -->

- [Install](#install)
- [skywire-systray usage](#skywire-systray-usage)

<!-- /MarkdownTOC -->

## Install

The skywire-systray interacts with the skywire-visor via skywire-cli via the default shell, and additionally interacts with with the scripts and services or batch files included with the skywire installation provided by the linux and mac packages or the windows .msi installer.

A desktop environment is required for the skywire-systray

```bash
$ cd $GOPATH/src/github.com/skycoin/skywire/cmd/skywire-systray
$ go install ./...
```

## skywire-systray usage

After the installation, you can run `skywire-systray` to see the usage:

```
$ skywire-systray
skywire systray

Usage:
  skywire-systray [flags]

Flags:
  -s, --src    'go run' using the skywire sources
  -d, --dev    show remote visors & dmsghttp ui
  -h, --help   help for skywire-systray

```

![skywire-systray](https://user-images.githubusercontent.com/36607567/184662776-d16f0660-9a05-4e4d-b769-5f17735f9644.png)

The skywire-systray can control the running state of the visor.

The linux implementation can update the visor's config via `skywire-autoconfig` when the visor is shut down. This will also start the visor

![skywire-systray1](https://user-images.githubusercontent.com/36607567/184664444-9b08a5ee-2e39-445d-8f7a-352d83fea777.png)
