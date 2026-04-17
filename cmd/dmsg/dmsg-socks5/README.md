# Dmsg socks5 proxy

A server and client are provided, which operate p2p over dmsg.

```
socks5 proxy to connect to socks5 server over dmsg

Usage:
  proxy client [flags]

Flags:
  -D, --dmsg-disc string   dmsg discovery url (default "http://dmsgd.skywire.skycoin.com")
  -q, --dport uint16       dmsg port to connect to socks5 server (default 1081)
  -k, --pk string          dmsg socks5 proxy server public key to connect to
  -p, --port int           TCP port to serve SOCKS5 proxy locally (default 1081)
  -s, --sk cipher.SecKey   a random key is generated if unspecified
 (default 0000000000000000000000000000000000000000000000000000000000000000)
```

```
dmsg proxy server

Usage:
  proxy server

Flags:
  -D, --dmsg-disc string   dmsg discovery url (default "http://dmsgd.skywire.skycoin.com")
  -q, --dport uint16       dmsg port to serve socks5 (default 1081)
  -s, --sk cipher.SecKey   a random key is generated if unspecified
 (default 0000000000000000000000000000000000000000000000000000000000000000)
  -w, --wl string          whitelist keys, comma separated


```

This utility is included primarily as a fallback mechanism to enable ssh connectivity for remote visors in the instance of routing failure for skywire.
