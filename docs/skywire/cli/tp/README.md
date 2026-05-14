# skywire cli tp

[← skywire cli](../README.md)

Display and manage transports of the local visor

	Transports are bidirectional communication protocols
	used between two Skywire Visors (or Transport Edges)

	Each Transport is represented as a unique 16 byte (128 bit)
	UUID value called the Transport ID
	and has a Transport Type that identifies
	a specific implementation of the Transport.

	Types: stcp stcpr sudph dmsg

## Usage

```
skywire cli tp
```

## Subcommands

- [add](add/README.md) — Add transport(s) to one or more remote public keys
- [all](all/README.md) — List all transports on the network
- [auto](auto/README.md) — Control public autoconnect
- [disc](disc/README.md) — Discover remote transport(s)
- [id](id/README.md) — Compute the deterministic transport ID for a given PK pair and type
- [metrics](metrics/README.md) — Transport discovery bandwidth metrics
- [net-stats](net-stats/README.md) — Network-wide transport statistics
- [rm](rm/README.md) — Remove transport(s) by id
- [tpd-health](tpd-health/README.md) — Transport discovery health and version info
- [tpd-stats](tpd-stats/README.md) — List visors by transport count from transport discovery
- [tree](tree/README.md) — tree map of transports on the skywire network
- [uptime](uptime/README.md) — query TPD integrated transport-level uptime
- [v](v/README.md) — List public visors
- [viz](viz/README.md) — Start transport discovery visualizer server

## Flags

```
  -t, --types strings     show transport(s) type(s) comma-separated
  -p, --pks strings       show transport(s) for public key(s) comma-separated
  -l, --logs              show transport logs (default true)
  -m, --more              show more info
  -b, --bw int            show bandwidth usage for last N days (0 = disabled)
      --inactive          show bandwidth for inactive transports (requires --bw)
      --cfu string        UT cache file location. (default "/tmp/ut.json")
      --cfsp string       SD cache file location (default "/tmp/proxysd.json")
      --cfsv string       SD cache file location (default "/tmp/vpnsd.json")
      --cfsvisor string   SD cache file location (default "/tmp/visorsd.json")
  -c, --cfa int           use cached service-discovery/UT data if younger than N minutes (default 5)
  -a, --sdurl string      service discovery url (default "http://sd.skycoin.com")
  -w, --uturl string      uptime tracker url (TPD integrated) (default "http://tpd.skywire.skycoin.com")
  -i, --id string         display transport matching ID
  -u, --tptypes           display transport types used by the local visor
  -s, --stats             show transport statistics (count by type, unique visors)
      --rpc string        RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
      --remote strings    list transports on remote visor(s) via TPS (comma-separated PKs)
  -L, --live              live-refresh mode (bubbletea TUI, 1s tick); shows transport bandwidth/latency updating in place. Skips --more service-disc fetches per tick; not compatible with --remote/--id/--tptypes
```

## Global Flags

```
      --json   print output as JSON
```

## Sample output

_Captured live from a running visor; output is truncated if it exceeds the per-command sample cap._

```
type      id                                       remote_pk                                                              mode        label
stcpr     004c3af6-ad2b-044b-8106-dcd70b125508     033a4623a1a6724fb8345422bb91d9cabd57c4468774ee12be8eea242317e78aa2     regular     automatic
stcpr     00a8cada-8843-0ba9-b14d-4cfb184616dc     02605a0612b3a898c834cba9fcc59de24ef7efc37366795be0488cfd9786e7b505     regular     automatic
stcpr     00c7dcf1-9f73-0226-92da-2fab98e84385     03f6ea1b41748fa9b40e2d11f5b97f918147f4433f0afd4bafbbc42bb5b80e630b     regular     automatic
stcpr     00f5837b-c2b3-0d79-a819-ae74912d5d27     024e5702e274b2df528f297f1a82cd32ce2fc8494bbf30fb871a8281e0918818b1     regular     automatic
stcpr     01a49b50-792c-0b7d-b951-bc907bceafef     03b6ead5ff68db237652c7ba5b3488927a752057f4796c1b104aa505c73ad57f27     regular     automatic
stcpr     01e1e730-c139-04a6-ae05-f5b61dfb8d4a     03c213d0fd4fce2f43025d4c1e5dd4b8a1338595a43eea566dc8fc8af769f8242e     regular     automatic
stcpr     01e2ab92-52fc-09f1-87f3-30b433367a73     025988c54037300ca0ae90a11b947413ad82a7a13c2751334be87ec57997f9297f     regular     automatic
stcpr     01f37b63-d50e-0845-9a96-c51ade36bcde     034f66068f6b3f92073351d1e8a5638c922bb180030ae591f8e5c401c3a0cf6b6a     regular     automatic
stcpr     022ee256-6f32-098f-94f0-ca2c1b05d523     02367fcd6f3776f0764a61274b77a037f42915e1a84c3de5539c9a4c8042016181     regular     automatic
stcpr     027d472f-f75b-0a3c-85c3-ad8069d50948     027da9eb9d7d0a51a38885c99bd0bb706feaf4f5b652ee63755d553c070ed9f44f     regular     automatic
stcpr     02912da6-4003-0639-b405-57e28c600e1b     03b467206197db22490ec9d44307feac8f8f8b7d340c86bfdca4e324560bbc6022     regular     automatic
stcpr     02b18330-ad92-0675-aeb3-548ecbfcd951     028d778d99df7bf2340c8e2dc2d250476bd33342b10c84dfc3f660e930faabbc08     regular     automatic
stcpr     02b2bb09-8304-0dc7-b568-6e8a438123ef     025ddae74235b313777e8e079ab50cff8ac5720707766d35e933fc5d78c9865eb6     regular     automatic
stcpr     02bbbff7-f67a-0910-a6ff-d32d0cd769ce     02086c9248f47908b0ca1eb9d6bfd29f16d6304bcbc475c2017f49e4868cf1e740     regular     automatic
stcpr     038b5e6e-54c1-0d63-b553-b2e288ed7a9d     0248a42a9e4d4379daa15af744f862ddea1cc10e9e21ebe2e5860b8888da89082a     regular     automatic
stcpr     03ec58f4-c699-0ba1-a9db-9e655faff3ff     026c93c8eac143e1d9f7459ac8720a377ede40a2070c77cd2f2cb2b6644e6b8892     regular     automatic
stcpr     046e0992-a039-0bd2-bc67-23d7800144ed     038483486b1c21304bd04b8d187452f25cb2e7fd7ad653429a79317c6c2399857e     regular     automatic
stcpr     0478bd08-9e7e-0c4b-b897-6ae617adbfe0     03827409b68b9d90f33f69204895bd0463710ccdb679177a8d83a64c4538d2270d     regular     automatic
stcpr     04d81530-7b01-049d-8633-fcd3f80b7f03     0306ad93b7602ef24438231f45ba93efe775ac6451cca4c07203152260c04fdf73     regular     automatic
stcpr     04f07ea9-e86c-0bfc-a526-4d339a57bb3e     02f06357152aa99f71b9d53015466dedc03f165727a6196add75232ebfe336f36a     regular     automatic
stcpr     04fac4b2-0f92-0b09-96ba-858777de20b8     0241b6d9a45d807e2a981f2d2b0116385f418629f5519b8934989211dc6fbed469     regular     automatic
stcpr     055356f1-5da3-0dc1-9d75-aa665250f9a4     023cbb106c1cd1e4eefac84373457a68b0a4a22ff4009e5916029af2130cfaa81f     regular     automatic
stcpr     0652628d-0efe-0a70-9c15-658111374ba1     03c5dabecae1fb2ba84eb1e3b46464b6e84a375616df68f4d3d0b7bb9614fc5528     regular     automatic
stcpr     069e769f-6850-05c2-8ccb-10989f5253b9     02bc38c63c2be6f28c1bfd85fdab22149287d20b5fdda00113f22b42ba3946c6fb     regular     automatic
stcpr     0749e9ce-20c0-01ad-83cd-9b765904ca96     025f83329c1ff6bbc827b8b0dbca70282e1352b183826499f6fb95f9abd1f1e801     regular     automatic
stcpr     074e45ce-3349-0704-b1b9-8f158d84e667     0346e702cb6a32fe744a704ebe5a8616de80e332a393542eee5072c7891940799e     regular     user
stcpr     07d3d5f4-7620-0cf7-a6a2-7890dd33b90d     02aba8108bdecc3c5f5b7402929451454ae0afa4941206749b87b11de79fccdcdc     regular     automatic
stcpr     08300642-6ed8-02d4-9ac4-e12bb012c9dc     033954445378d7c0c941f9405498ef8b4f0e3bc6ced8f91144d8ec7f111882d48b     regular     user
stcpr     08814e05-d3ec-0a83-80e6-dc0e04f768a7     030d530ee2bcfbce478e04afaa52a0d6947a3a4080f8bdc73adcb6361e313bef1a     regular     automatic
... (676 more lines)
```

---
_Generated by `skywire doc` — do not edit by hand._
