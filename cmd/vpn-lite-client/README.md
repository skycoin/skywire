# Skywire VPN lite client app

`vpn-lite-client` app implements lite client for the VPN server app.

It opens persistent `skywire` connection to the configured remote visor. The functionalities like rules setup, direct routes, system privilages and rerouting system trafic are stripped in the light version. The purpose of this app is to only create a TUN interface with the `vpn-server` acting as a normal `vpn-client` in order to test the connection with the `vpn-server`. Since this is used only in network monitor it is masqueraded as a normal `vpn-client` in order to not add any more code to the `vpn-server`.

## Configuration

Additional arguments may be passed to the application via `args` array. These are:
- `-srv` (required) - is a public key of the remove VPN server;

Full config of the client should look like this:
```json5
{
  "app": "vpn-client",
  "auto_start": false,
  "port": 43,
  "args": [
    "-srv",
    "03e9019b3caa021dbee1c23e6295c6034ab4623aec50802fcfdd19764568e2958d",
  ]
}
```