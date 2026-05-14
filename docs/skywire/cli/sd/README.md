# skywire cli sd

[← skywire cli](../README.md)

Display combined service discovery and transport statistics

Combines data from:
- Service Discovery: http://sd.skycoin.com/api/services
- Transport Discovery: http://tpd.skywire.skycoin.com/all-transports

Shows public keys with their services and transport counts by type.

Use --testenv or SKYWIRETEST=1 to use test deployment services.

## Usage

```
skywire cli sd
```

## Flags

```
      --cds string       SD cache dir ("" to disable) (default "/tmp/sd.skycoin.com")
      --cdt string       TPD cache dir ("" to disable) (default "/tmp/tpd.skywire.skycoin.com")
      --cdu string       UT cache dir ("" to disable) (default "/tmp/ut.skywire.skycoin.com")
  -m, --cfa int          update cache files if older than n minutes (default 5)
  -c, --country string   filter by country code
      --json             print output in json
  -n, --min int          filter by minimum transport count
      --no-cxo           skip CXO subscriber-cache step
      --no-dmsg          skip direct DMSG HTTP step
      --no-http          skip direct HTTP fallback step
      --no-rpc           skip visor RPC (DmsgHTTP) step
  -o, --noton            do not filter by online status in UT
      --rpc string       RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
  -a, --sdurl string     service discovery url (default "http://sd.skycoin.com")
      --testenv          use test deployment
  -b, --tpdurl string    transport discovery url (default "http://tpd.skywire.skycoin.com")
  -w, --uturl string     uptime tracker url (default "http://ut.skywire.skycoin.com")
  -e, --version string   filter by version
```

## Sample output

_Captured live from a running visor; output is truncated if it exceeds the per-command sample cap._

```
Legend: *offline *not in UT *online <2 transports

pk                                                                         country   version                         services          stcpr   sudph   dmsg   stcp   total
03a590eac67f4f02b89a60de8b202d6702080b7a2ab41a5796afe3cb865370ebb9:43611   DE        v1.3.54-0                       visor             447     116     0      0      563
02f9aa588dffa20b205e1c10bd0236130f080af157044d0eaa35753d2f2fcd6c36:42597   DE        v1.3.54-0.-c0beaf0a56f7+dirty   visor             508     5       0      0      513
027087fe40d97f7f0be4a0dc768462ddbb371d4b9e7679d4f11f117d757b9856ed:7778    US        v1.3.54-0                       visor             465     17      0      0      482
0323272a60895f56aad82cb767fb5c413807adcf7c9fb0578b1b1c5807c7f29d4c:7773    US        v1.3.54-0.-6b0661cea4d3+dirty   visor             476     4       0      0      480
032a07b993838c7af418fceeb5f67586edb9a211844291e8b10be14aaad2df83c2                                                                     4       58      0      0      62
03011c12ee26fa9ae7c00bbe128ccc6323d7b2a6568f86c2e4206e6f70a1688a54         ID        v1.3.53                         proxy,vpn         4       56      0      0      60
022878223eddf01ab5730780d76d90c8eb060fbacb12636e94ca7c39de221b0bb3         ID        v1.3.53                         proxy,vpn         4       56      0      0      60
0276fbf7a21195259812a7379ff8763b5b9935ac3daec52018c62cad059b4cdd10         ID        v1.3.53                         proxy,vpn         5       54      0      0      59
036b72415315165453a0ac25fe39d4c114d52f93547b37d48eae02c8e59d79b781         ID        v1.3.53                         proxy,vpn         4       55      0      0      59
03dc3488e49ded9250f3cca2827ab16f6331b7ccefdad614353c785a5fc76c1b13         IT        v1.3.47                         proxy             57      0       0      0      57
03a6003e894e86dab66bf333e36d39c983d35a8a5f97ed0a7e7a68caf7eed57ad8         ID        v1.3.53                         proxy,vpn         4       53      0      0      57
02b6d87d45e433496a8120f6a537b92e7f2a86711e74f4d937f83eb860d9b5d818         ID        v1.3.53                         proxy,vpn         4       52      0      0      56
0332a0fcf643a9d4a56ba252e6a6e67bab885d0cb4ec942f8b93c40c0df5152b09         ID        v1.3.36                         proxy,vpn         0       56      0      0      56
02c0002c02f0453001695fd274c5cad3d24a0cdd81d7da065234037d0b793e572e         NL        v1.3.53                         proxy,vpn         4       52      0      0      56
03c77e4febd6823a83c742190121cc2bd65a4d65b9fcfef1f694682769b13a85c7         ID        v1.3.53                         proxy,vpn         4       49      0      0      53
034f66068f6b3f92073351d1e8a5638c922bb180030ae591f8e5c401c3a0cf6b6a         ID        v1.3.53                         proxy,vpn         4       48      0      0      52
03efa5bdc8ec74831af1cd0b450bc5c937804cecd36654b1f435712d958f963c9f         ID        v1.3.53                         proxy,vpn         4       47      0      0      51
026747f6f103146d76d592093e7634de4145eac0c74e7d8a6294a65aabe5fd3a44         ID        v1.3.53                         proxy,vpn         4       45      0      0      49
021bb7c830359bfd0f29859369c809d06394ff320315c5073b8eec24f05a47f843         ID        v1.3.53                         proxy,vpn         4       43      0      0      47
0277b420e9abeae438a98c63c175ad3f5ba6f02181eb230f1b00eaf16858eef71b         NL        v1.3.53                         proxy,vpn         6       39      0      0      45
02fab97bd82b0664420f7d62b7a7f97372aa03c0004be7fdd304c5f20d8786f884         US        v1.3.36                         proxy,vpn         0       44      0      0      44
038ab4af9290de7688cb6dd827c2a601a28ef25894ea5da1105b9bd89f5d25f946         ID        v1.3.53                         proxy,vpn         4       40      0      0      44
02354c9fb93bce030d5463f44ddd474549c9d8202ac7c4348520e89821aaa04e58         ID        v1.3.53                         proxy,vpn         4       40      0      0      44
022a8ec8df7e52e0375de2da3e6196ceb93f7d0bbab914c99fd67a9cb04baff631         ID        v1.3.37                         proxy,vpn         0       42      0      0      42
037692a6bf91c754e1a4a129fb57273bc381480c29fc60bd183d3653e9dc6bee29         ID        v1.3.53                         proxy,vpn         4       38      0      0      42
03c5dabecae1fb2ba84eb1e3b46464b6e84a375616df68f4d3d0b7bb9614fc5528         NL        v1.3.53                         proxy,vpn         5       36      0      0      41
03679f2496b89f4dcb92e05c6e429347e7d5e0b6aec072eb0bdf7c0c163628292a         ID        v1.3.53                         proxy,vpn         4       36      0      0      40
... (960 more lines)
```

---
_Generated by `skywire doc` — do not edit by hand._
