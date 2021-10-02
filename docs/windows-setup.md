### Windows

Prequisites:

- `gcc` (for systray)
- `git`
- `go`
- `make`
- `powershell` (we don't support running it from `CMD` at the moment)

Skywire visor may be run on Windows, but this process requires some manual operations at the moment.

In order to run the skywire visor on Windows, you will need to build it first:

```powershell
> make build-windows 
```

Apps should be declared in the config without `.exe` extension.

Change the encoding of the logs in your terminal to increase legibility.

```
CHCP 65001
```

To run it, you can generate a config first via:

```powershell
> .\skywire-cli.exe visor gen-config -t
```

It will create a file called `skywire-config.json` on the root directory of this project.

Then you can run the visor via:

```powershell
> .\skywire-visor.exe -c .\skywire-config.json
```

#### Unsupported features

- `syslog`

Using `syslog` is currently not supported on Windows.

#### Partially Supported Features

- `dmsgpty`

Will only work on Windows Server 2019 and Windows 10.

#### Running a VPN-client

Running the Skywire VPN-client on Windows requires the `wintun` driver to be installed. You can either install the driver itself or install `Wireguard` which includes the driver.

The VPN client requires `local system` user rights to be run. You can set these with `PsExec`: https://docs.microsoft.com/en-us/sysinternals/downloads/psexec . To start your terminal with `local system` rights use:

```
PsExec.exe -i -s C:\Windows\system32\cmd.exe
```

Run Skywire from this terminal Window. 