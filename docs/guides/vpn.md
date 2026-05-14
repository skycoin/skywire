# Using the Skywire VPN

The following documentation exists for VPN server / client setup and usage:
- [Setup the Skywire VPN](https://github.com/skycoin/skywire/wiki/Skywire-VPN-Client)
- [Setup the Skywire VPN server](https://github.com/skycoin/skywire/wiki/Skywire-VPN-Server)
- [Package Installation Guide](https://github.com/skycoin/skywire/wiki/Skywire-Package-Installation)

VPN client and server permissions are documented separately in
[permissions.md](permissions.md).

An example using the VPN with `skywire cli`:

```
skywire cli vpn list
```
This will query the service discovery for a list of VPN server public keys:
[sd.skycoin.com/api/services?type=vpn](https://sd.skycoin.com/api/services?type=vpn)

Sample output:
```
02836f9a39e38120f338dbc98c96ee2b1ffd73420259d1fb134a2d0a15c8b66ceb
0289a464f485ce9036f6267db10e5b6eaabd3972a25a7c2387f92b187d313aaf5e
03cad59c029fc2394e564d0d328e35db17f79feee50c33980f3ab31869dc05217b
02cf90f3b3001971cfb2b2df597200da525d359f4cf9828dca667ffe07f59f8225
03e540ddb3ac61385d6be64b38eeef806d8de9273d29d7eabb8daccaf4cee945ab
...
```

Select a key and start the VPN with:
```
skywire cli vpn start <public-key>
```

View the status of the VPN:
```
skywire cli vpn status
```

Check your IP address with [ip.skywire.dev](https://ip.skywire.dev).

_Note: ip.skycoin.com will only show your real IP address, not the IP
address of the VPN connection._

Stop the VPN:
```
skywire cli vpn stop
```

_Note: killswitch may be configured for the VPN — see
`skywire cli config gen --all` help menu or the configuration
documentation._
