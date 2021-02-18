### Windows

Skywire visor may be run on Windows, but this process requires some manual operations at the moment.

In order to run the skywire visor on Windows, you will need to manually build it. Do not forget the `.exe` extensions.

`go build -o ./skywire-visor.exe ./cmd/skywire-visor`

Apps may be built the same way:

`go build -o ./apps/vpn-client.exe ./cmd/apps/vpn-client`

Apps should be declared in the config without `.exe` extension.

Change the encoding of the logs in your terminal to increase legibility.

```
CHCP 65001
```

#### Unsupported features

- `dmsgpty`
- `syslog`

Using `dmsgpty` and `syslog` is currently not supported on Windows.

#### Running a VPN-client

Running the Skywire VPN-client on Windows requires the `wintun` driver to be installed. You can either install the driver itself or install `Wireguard` which includes the driver.

The VPN client requires `local system` user rights to be run. You can set these with `PsExec`: https://docs.microsoft.com/en-us/sysinternals/downloads/psexec . To start your terminal with `local system` rights use:

```
PsExec.exe -i -s C:\Windows\system32\cmd.exe
```

Run Skywire from this terminal Window. 