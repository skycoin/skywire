# Skywire VPN client app

`vpn-client` app implements client for the VPN server app.

It opens persistent `skywire` connection to the configured remote visor. This connection is used as a tunnel. Client forwards all the traffic through that tunnel to the VPN server.

## Configuration

Additional arguments may be passed to the application via `args` array. These are:
- `-srv` (required) - is a public key of the remove VPN server;
- `-passcode` - passcode to authenticate connection. Optional, may be omitted.

Full config of the client should look like this:
```json5
{
  "app": "vpn-client",
  "auto_start": false,
  "port": 43,
  "args": [
    "-srv",
    "03e9019b3caa021dbee1c23e6295c6034ab4623aec50802fcfdd19764568e2958d",
    "-passcode",
    "1234"
  ]
}
```

## Running app

Compile app binary and start a visor:

```sh
$ go build -o apps/vpn-client ./cmd/apps/vpn-client
$ ./skywire-visor skywire-config.json
```

You should be able to see an additional hop with the `traceroute`-like utils:

```sh
$ traceroute google.com
```

Also, your IP should be detected as the IP of the VPN server:

```sh
$ curl https://api.ipify.org
```