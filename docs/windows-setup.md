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


To run it, you can generate a config first via:

```powershell
> .\skywire-cli.exe config gen -t --is-hypervisor -r -o .\skywire-config.json
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

Running the Skywire VPN-client on Windows requires the `wintun` version `0.14` driver to be installed. 

- Download it from [here](https://wintun.net/builds/wintun-0.14.zip)
- Extract the file
- Copy the `wintun.dll` in the `wintun\bin\<YOUR_ARCH>\wintun.dll` to the `C:\Windows\System32\wintun.dll`
- For better output (formatted logs, etc), install [Windows Terminal](https://github.com/microsoft/terminal)
- Run windows terminal as administrator and run

```powershell 
> .\skywire-visor.exe -c .\skywire-config.json
```

- You can follow the rest of the guide for connecting the VPN Client in skywire wiki.
