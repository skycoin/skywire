# Skywire VPN server app

`vpn-server` app implements VPN functionality over skywire net.

Currently the server supports authentication with a passcode that is set in the configuration file.
If none is provided, the server does not require authentication.

NOTE: in contrast with the proxy apps, VPN server and client must be run on different machines because of the networking features.

## Configuration

Additional arguments may be passed to the application via `args` array. These are:
- `-passcode` - passcode to authenticate incoming connections. Optional, may be omitted.

Full config of the server should look like this:
```json5
{
  "app": "vpn-server",
  "auto_start": true,
  "port": 44,
  "args": [
    "-passcode",
    "1234"
  ]
}
```

## Running app

Compile app binary and start a visor:

```sh
$ go build -o apps/vpn-server ./cmd/apps/vpn-server
$ ./skywire-visor skywire-config.json
```