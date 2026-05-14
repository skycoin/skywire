# Using the Skywire SOCKS5 proxy client

The following wiki documentation exists on the SOCKS5 proxy:
- [Skywire SOCKS5 Proxy User Guide](https://github.com/skycoin/skywire/wiki/Skywire-SOCKS5-Proxy-User-Guide)
- [SSH over SOCKS5 Proxy](https://github.com/skycoin/skywire/wiki/SSH-over-SOCKS5-Proxy)

The main difference between the VPN and the SOCKS5 proxy is that the
proxy is configured _per application_ while the VPN wraps the
connections for the whole machine.

The socks client usage (from `skywire cli`) is similar to the VPN, though
the `skywire cli` subcommands and flags do not currently match from the
one application to the other. This will be rectified.

To use the SOCKS5 proxy client via `skywire cli`:
```
skywire cli proxy list
```
This will query the service discovery for a list of visor public keys
which are running the proxy server.
[sd.skycoin.com/api/services?type=proxy](https://sd.skycoin.com/api/services?type=proxy)

Sample output:
```
031a924f5fb38d26fd8d795a498ae53f14782bc9f036f8ff283c479ac41af95ebd
024fdf44c126e122f09d591c8071a7355d4be9c561f85ea584e8ffe4e1ae8717f7
03ae05142dcf5aad70d1b58ea142476bac49874bfaa67a1369f601e0eb2f5842df
0313a76e2c331669a0cb1a3b749930881f9881cca89b59ee52365d1c15141d9d83
03022fa8a0c38d20fae9335ef6aa780f5d762e1e161e607882923dc0d5a890f094
03e4b6326f9df0cff1372f52906a6d1ee03cf972338d532e17470e759362e45c87
0230689d26e5450e8c44faaba91813b7c2b00c1add3ad251e2d62ecca8041a849d
036ae558d5e6c5fc73cb6a329cb0006b4f659ecf9ae69c9e38996dfb65b1fb1c45
03a35c742ed17506834235b2256bb2b0a687de992e5ded52ca4d54fba3b00b8dbe
0259721a9e79e91ce8bc94bad52a6a381d50fcb05aaadc2c99201fd137fb71dfde
...
```

Select a key and start the proxy with:
```
skywire cli proxy start --pk <public-key>
```

View the status of the proxy:
```
skywire cli proxy status
```

Check the IP address of the connection — for example, using `curl` via the socks5 proxy connection:
```
curl -Lx socks5h://127.0.0.1:1080 http://ip.skycoin.com/ | jq
```

The connection may be consumed in a web browser via direct proxy
configuration in browsers which support it, or using extensions such
as `foxyproxy`.

The connection may also be consumed in the terminal by setting `ALL_PROXY`
environmental variable, or via the specific method used by a certain
application.

Examples of `ssh` over the socks5 proxy:

Using `openbsd-netcat`:
```
ssh user@host -p 22 -o "ProxyCommand=nc -X 5 -x 127.0.0.1:1080 %h %p"
```

Using `ncat` from `nmap`:
```
ssh user@host -p 22 -o "ProxyCommand=ncat --proxy-type socks5 --proxy 127.0.0.1:1080 %h %p"
```

Stop the socks5 proxy client:
```
skywire cli proxy stop
```
