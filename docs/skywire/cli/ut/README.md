# skywire cli ut

[← skywire cli](../README.md)

query uptime tracker

http://ut.skywire.skycoin.com/uptimes?v=v2

Check local visor daily uptime percent with:

$ skywire-cli ut -n0 -k $(skywire-cli visor pk)

Set cache dir to "" to avoid using cache files

Use --testenv or SKYWIRETEST=1 to use test deployment services.

## Usage

```
skywire cli ut
```

## Subcommands

- [mdisc](mdisc/README.md) — query DMSG-discovery integrated uptime tracker
- [sd](sd/README.md) — query service-discovery integrated uptime tracker
- [tpd](tpd/README.md) — query TPD integrated uptime tracker

## Flags

```
      --cdt string           TPD cache dir ("" to disable)
                              (default "/tmp/tpd.skywire.skycoin.com")
      --cdu string           UT cache dir ("" to disable)
                              (default "/tmp/ut.skywire.skycoin.com")
  -m, --cfa int              update cache files if older than n minutes
                              (default 5)
      --date string          only output uptime for this date (YYYY-MM-DD); reduces 7-day response to a single day
  -l, --list-versions        list PKs with their versions
      --max-tp int           filter visors with at most N transports (fetches TPD data) (default -1)
  -n, --min int              list visors meeting minimum uptime percentage
                              (default 75)
      --min-version string   filter visors with version >= specified (e.g. v1.3.34)
      --no-cxo               skip CXO subscriber-cache step
      --no-dmsg              skip direct DMSG HTTP step
      --no-http              skip direct HTTP fallback step
      --no-rpc               skip visor RPC (DmsgHTTP) step
  -o, --on                   list currently online visors
  -k, --pk string            check uptime for the specified key
      --rpc string           RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
  -s, --stats                count the number of results
  -t, --stats2               count of versions
      --testenv              use test deployment
      --tpdurl string        transport discovery url (default "http://tpd.skywire.skycoin.com")
  -u, --url string           specify alternative uptime tracker url
                              (default "http://ut.skywire.skycoin.com")
  -v, --version string       filter visors by exact version
```

## Global Flags

```
      --json   print output as JSON
```

## Sample output

_Captured live from a running visor; output is truncated if it exceeds the per-command sample cap._

```
0200b3c311dba7c90ae6661362534a9e68f2428ca89631cd5fff93143a283a1905 2026-05-08 79.17
0200b3c311dba7c90ae6661362534a9e68f2428ca89631cd5fff93143a283a1905 2026-05-09 100.00
0200b3c311dba7c90ae6661362534a9e68f2428ca89631cd5fff93143a283a1905 2026-05-10 83.68
0200b3c311dba7c90ae6661362534a9e68f2428ca89631cd5fff93143a283a1905 2026-05-11 80.90
0200b3c311dba7c90ae6661362534a9e68f2428ca89631cd5fff93143a283a1905 2026-05-12 97.57
0200b3c311dba7c90ae6661362534a9e68f2428ca89631cd5fff93143a283a1905 2026-05-13 100.00
02018696c4715aae87f77f0f0b4ac6348c25f21a5469794ff7a9c84d448d2a525a 2026-05-08 100.00
02018696c4715aae87f77f0f0b4ac6348c25f21a5469794ff7a9c84d448d2a525a 2026-05-09 97.92
02018696c4715aae87f77f0f0b4ac6348c25f21a5469794ff7a9c84d448d2a525a 2026-05-10 89.93
02018696c4715aae87f77f0f0b4ac6348c25f21a5469794ff7a9c84d448d2a525a 2026-05-11 100.00
02018696c4715aae87f77f0f0b4ac6348c25f21a5469794ff7a9c84d448d2a525a 2026-05-12 100.00
02018696c4715aae87f77f0f0b4ac6348c25f21a5469794ff7a9c84d448d2a525a 2026-05-13 100.00
0201a9add997bf1681fd9fe1c7125f081edbde8af2b69b2338e245290035669c02 2026-05-08 99.31
0201a9add997bf1681fd9fe1c7125f081edbde8af2b69b2338e245290035669c02 2026-05-10 95.83
0201a9add997bf1681fd9fe1c7125f081edbde8af2b69b2338e245290035669c02 2026-05-11 97.92
0201a9add997bf1681fd9fe1c7125f081edbde8af2b69b2338e245290035669c02 2026-05-12 95.83
0201a9add997bf1681fd9fe1c7125f081edbde8af2b69b2338e245290035669c02 2026-05-13 93.40
0203522e92b0ee7ea4c860bff55d353025b79fa2d34b198c26699458a5f29118fb 2026-05-08 100.00
0203522e92b0ee7ea4c860bff55d353025b79fa2d34b198c26699458a5f29118fb 2026-05-09 100.00
0203522e92b0ee7ea4c860bff55d353025b79fa2d34b198c26699458a5f29118fb 2026-05-10 97.57
0203522e92b0ee7ea4c860bff55d353025b79fa2d34b198c26699458a5f29118fb 2026-05-11 100.00
0203522e92b0ee7ea4c860bff55d353025b79fa2d34b198c26699458a5f29118fb 2026-05-12 100.00
0203522e92b0ee7ea4c860bff55d353025b79fa2d34b198c26699458a5f29118fb 2026-05-13 88.54
0203b5dda5089a4a200f723650fd121b1db550f412f1ffe0e741e00e082f3d457e 2026-05-08 86.81
0203b5dda5089a4a200f723650fd121b1db550f412f1ffe0e741e00e082f3d457e 2026-05-09 77.08
0203b5dda5089a4a200f723650fd121b1db550f412f1ffe0e741e00e082f3d457e 2026-05-11 85.07
0203b5dda5089a4a200f723650fd121b1db550f412f1ffe0e741e00e082f3d457e 2026-05-12 80.56
0203b5dda5089a4a200f723650fd121b1db550f412f1ffe0e741e00e082f3d457e 2026-05-13 82.99
0203d0b6668b1355c7afc3a6d88d70c7a437edd6e79201a32c30a6df58d7072d75 2026-05-08 99.31
0203d0b6668b1355c7afc3a6d88d70c7a437edd6e79201a32c30a6df58d7072d75 2026-05-10 95.83
... (8040 more lines)
```

---
_Generated by `skywire doc` — do not edit by hand._
