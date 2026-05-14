# skywire cli ut tpd

[← skywire cli ut](../README.md)

Query the Transport-Discovery integrated uptime endpoint.

http://tpd.skywire.skycoin.com/uptimes

Same response shape as sd / mdisc / ut — v1 (pk+on), v2 (+ daily %),
v3 (+ per-5-minute timeline bitmap). Default is v2; pass -T / --timeline
to request v3 and render the bitmap as 24 hourly blocks.

Populated by visor heartbeats + transport registrations; the same
data feeds into the rewards pipeline.

## Usage

```
skywire cli ut tpd
```

## Subcommands

- [graph](graph/README.md) — render an uptime timeline graph (compact shaded-block bars)

## Flags

```
  -a, --all                  include every day the server returned
  -m, --cache-age int        re-fetch if cache is older than N minutes (0 disables) (default 5)
      --cache-dir string     cache directory ("" disables cache) (default "/tmp/tpd.skywire.skycoin.com")
  -d, --days int             number of most-recent days to include (0 = latest day only)
      --json                 emit raw JSON
  -l, --list-versions        list version distribution (with --stats) or pk+version pairs
  -n, --min-daily int        only visors whose worst daily-uptime is >= this percent
      --min-version string   filter visors with version >= this (e.g. v1.3.40)
      --no-cxo               skip CXO subscriber-cache step
      --no-dmsg              skip direct DMSG HTTP step
      --no-http              skip direct HTTP fallback step
      --no-rpc               skip visor RPC (DmsgHTTP) step
  -o, --on                   only include online visors
  -k, --pk string            only show PKs matching this substring
      --since string         include days on or after this date (YYYY-MM-DD)
  -s, --stats                print count of matching visors only
      --timeout duration     HTTP timeout (default 30s)
      --until string         include days on or before this date (YYYY-MM-DD)
      --url string           discovery base URL (default "http://tpd.skywire.skycoin.com")
  -v, --v string             response version (v1|v2) (default "v2")
      --version string       filter visors by exact version
      --visors strings       server-side filter: only return these PKs (comma-separated)
```

## Sample output

_Captured live from a running visor; output is truncated if it exceeds the per-command sample cap._

```
pk                                                                  ver                            on   2026-05-14
0201a9add997bf1681fd9fe1c7125f081edbde8af2b69b2338e245290035669c02  v1.3.51                        yes  85.31
0203522e92b0ee7ea4c860bff55d353025b79fa2d34b198c26699458a5f29118fb  v1.3.47                        yes  35.00
0203d0b6668b1355c7afc3a6d88d70c7a437edd6e79201a32c30a6df58d7072d75  v1.3.51                        yes  84.48
020415335d295854c110aca04aae1cf60abc13db9e2bd52604f25109708123907c  v1.3.51                        no   39.38
020444a70f2a8a1293ff1f2ee4a6fbc8b287e55fcf1c343e49445a0731c43c972f  -                              no   21.04
020456fbd93bbd27a59a273aa89a25fdff61f208c7b30417af33949e5deb6e7fa8  v1.3.45                        no   58.75
02049616203ceaf3cd5085bd3075197a75ecaf20e2d3ec73edda6984cbbea8c7fb  -                              no   22.08
0204a69678a8c9b8ed81066bfaa03797d35e3594b636e46f216fd306636c0d3dfd  -                              no   23.12
020514fe717f7c2a38b3a501bf23de2f152e0184b1ee1fc42b5fdd8381540c5050  -                              no   22.19
02056c545c6b2c361957fb4a34254c3a53404e66e90800f10fd44ca4e41cd544a5  v1.3.51                        no   86.35
0205ab636bc3a546969099fda408b9e698545904930b964c0a32956e90d1d4529a  v1.3.51                        no   84.27
02060795ffd569dc25029c105c83357bfeee6b30478229614a0a9831ebc2e7664b  v1.3.47                        yes  37.81
02060e5aa22af790ac616bf8bdfa328a29f7db66e7ded2832b72b832864c0f69f0  v1.3.45                        yes  57.40
020740a986dc8a7607982b8d29569e74c11946b53b23f6d26417882aeb3a573436  v1.3.47                        no   81.35
0207abc28c19492fd22ffc39985982bbb2b76b892a6c11b64d03ba9adeaf520f80  v1.3.51                        no   24.58
0207e9cda5cc2811a1e31aa552cc3d268ac0eea3c7c9198d40e2da91274b4c0a6c  -                              no   21.15
02086c9248f47908b0ca1eb9d6bfd29f16d6304bcbc475c2017f49e4868cf1e740  v1.3.47                        yes  40.52
0208cbf61175699bd22367ab1104105c2d432117d896394cbe522fa5fb2bf3ae7f  v1.3.47                        yes  79.58
0208d0b9da545ea2e27a5e2035bc6339f93f8d65dfdf6931a0810475cc09787145  v1.3.51                        no   77.50
02097dd0634a3371e58165ad4d7ef9557c306daff53244fb2612fd2dcc4548424a  -                              no   18.23
0209ff385cd7b86752f626d976d4fd796b603004e7b1d1a3727156827eee1f0585  v1.3.51                        yes  86.56
020a4c4fe187e43b1d391e62217e430eafb470770ca1f294b0daff3130d6e65210  v1.3.47                        yes  20.62
020ad929d8cb28661380adb25d8481358fdabafb106258ba3ddac44547bf744ec5  -                              no   -
020af3617300003e8c76c929367a632895ed0e679b15fcf3c3f43ace23da090057  v1.3.47                        yes  30.00
020b38144399194dcc2fc670e0dc7dd89f7b2bb20c463c801fa23795ceaceb13f6  v1.3.47                        yes  27.50
020b4b38a25c74c5001a6e9196acac89343e7779a2d616f217ccf8eeaca49f1196  v1.3.51                        no   21.88
020b5b6ba5422d38e466009ccf78853bc4028e9a0bcbcdfa13d8ea3da61bd23c47  v1.3.39                        yes  90.73
020bab9b467534c790263cd2ee57e15c8a8411a79e01d91137382af966a1c075ae  v1.3.51                        no   23.12
020c632598e5a16314d2ac0a88ae8718c6f7b2d2312ec3e3e9e4e1b95811740ad2  -                              no   18.96
... (1236 more lines)
```

---
_Generated by `skywire doc` — do not edit by hand._
