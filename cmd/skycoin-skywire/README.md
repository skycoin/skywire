# Skywire + Skycoin

Combined binary integrating Skywire networking with Skycoin wallet and blockchain tools.

## Subcommand Tree

```
skywire
├── app
│   ├── skychat
│   ├── skynet-client
│   ├── skynet-srv
│   ├── skysocks
│   ├── skysocks-client
│   ├── vpn-client
│   └── vpn-server
├── cli
│   ├── completion
│   ├── config
│   │   ├── check-pk
│   │   ├── gen
│   │   ├── gen-keys
│   │   ├── parse
│   │   ├── pk
│   │   ├── show
│   │   └── update
│   │       ├── hv
│   │       ├── sc
│   │       ├── ss
│   │       ├── svc
│   │       ├── vpnc
│   │       └── vpns
│   ├── dmsg
│   │   ├── connect-all
│   │   ├── curl
│   │   ├── probe
│   │   ├── pty
│   │   │   ├── list
│   │   │   ├── start
│   │   │   ├── ui
│   │   │   └── url
│   │   ├── sessions
│   │   └── set-sessions
│   ├── gotop
│   ├── log
│   │   ├── st
│   │   └── tp
│   ├── mdisc
│   │   ├── entry
│   │   └── servers
│   ├── proxy
│   │   ├── list
│   │   ├── route
│   │   │   ├── add
│   │   │   └── remove
│   │   ├── server
│   │   │   ├── start
│   │   │   ├── status
│   │   │   └── stop
│   │   ├── start
│   │   ├── status
│   │   ├── stop
│   │   └── test
│   ├── pv
│   ├── reward
│   │   └── rules
│   ├── rewards
│   │   ├── bot
│   │   ├── bw-collect
│   │   ├── loginchain
│   │   ├── script
│   │   │   ├── getlogs
│   │   │   └── reward
│   │   ├── svc
│   │   ├── systemd
│   │   ├── tp-collect
│   │   └── ui
│   ├── rg
│   │   └── ls
│   ├── route
│   │   ├── add
│   │   │   ├── a
│   │   │   ├── b
│   │   │   └── c
│   │   ├── calc
│   │   ├── find
│   │   ├── groups
│   │   ├── rm
│   │   └── rsn-stats
│   ├── sd
│   ├── skychat
│   │   ├── listen
│   │   └── send
│   ├── skynet
│   │   ├── curl
│   │   ├── port
│   │   │   ├── add
│   │   │   ├── ls
│   │   │   └── rm
│   │   ├── srv
│   │   │   ├── start
│   │   │   ├── status
│   │   │   └── stop
│   │   ├── start
│   │   ├── status
│   │   └── stop
│   ├── survey
│   ├── svc
│   │   ├── ar
│   │   ├── dmsgd
│   │   │   ├── all-servers
│   │   │   ├── clients
│   │   │   └── server-clients
│   │   ├── health
│   │   ├── nm
│   │   └── tpd
│   │       ├── bandwidth
│   │       ├── bandwidth-tp
│   │       ├── metrics-tp
│   │       ├── metrics-visor
│   │       ├── per-key-stats
│   │       ├── stats
│   │       ├── versions
│   │       ├── versions-pk
│   │       └── visor-stats
│   ├── tp
│   │   ├── add
│   │   │   ├── edge
│   │   │   └── pv
│   │   ├── auto
│   │   ├── disc
│   │   ├── id
│   │   ├── metrics
│   │   ├── net-stats
│   │   ├── rm
│   │   ├── sync
│   │   ├── tpd-health
│   │   ├── tpd-stats
│   │   ├── tree
│   │   ├── uptime
│   │   ├── v
│   │   └── viz
│   ├── tps
│   │   ├── add
│   │   ├── list
│   │   └── rm
│   ├── ut
│   │   ├── mdisc
│   │   │   └── graph
│   │   ├── sd
│   │   │   └── graph
│   │   └── tpd
│   │       └── graph
│   ├── util
│   │   ├── edit
│   │   ├── got
│   │   │   ├── dl
│   │   │   ├── head
│   │   │   └── req
│   │   ├── jq
│   │   └── serve
│   ├── visor
│   │   ├── app
│   │   │   ├── arg
│   │   │   │   ├── autostart
│   │   │   │   ├── killswitch
│   │   │   │   ├── netifc
│   │   │   │   ├── passcode
│   │   │   │   └── secure
│   │   │   ├── deregister
│   │   │   ├── log
│   │   │   ├── ls
│   │   │   ├── register
│   │   │   ├── start
│   │   │   └── stop
│   │   ├── dmsg-servers
│   │   ├── go
│   │   ├── halt
│   │   ├── hv
│   │   │   ├── cpk
│   │   │   ├── disable
│   │   │   ├── enable
│   │   │   ├── pk
│   │   │   ├── status
│   │   │   └── ui
│   │   ├── info
│   │   ├── ip
│   │   ├── log
│   │   ├── ping
│   │   │   ├── bandwidth
│   │   │   ├── graph
│   │   │   ├── stop-all
│   │   │   ├── test
│   │   │   ├── tree
│   │   │   └── tree2
│   │   ├── pk
│   │   ├── ports
│   │   ├── proxies
│   │   │   ├── set
│   │   │   └── upstream
│   │   ├── ready
│   │   ├── reinit
│   │   ├── reward
│   │   ├── start
│   │   ├── user
│   │   └── ver
│   └── vpn
│       ├── list
│       ├── server
│       │   ├── start
│       │   ├── status
│       │   └── stop
│       ├── start
│       ├── status
│       ├── stop
│       ├── ui
│       └── url
├── completion
│   ├── bash
│   ├── fish
│   ├── powershell
│   └── zsh
├── cxo
│   ├── cli
│   │   ├── connection
│   │   │   ├── list
│   │   │   └── list-by-feed
│   │   ├── feed
│   │   │   ├── is-sharing
│   │   │   ├── list
│   │   │   ├── share
│   │   │   └── unshare
│   │   ├── kv
│   │   │   ├── create
│   │   │   ├── delete
│   │   │   ├── get
│   │   │   ├── list
│   │   │   └── put
│   │   ├── root
│   │   │   ├── info
│   │   │   ├── last
│   │   │   └── tree
│   │   ├── stat
│   │   ├── stop
│   │   ├── tcp
│   │   │   ├── address
│   │   │   ├── connect
│   │   │   ├── disconnect
│   │   │   ├── subscribe
│   │   │   └── unsubscribe
│   │   └── udp
│   │       ├── address
│   │       ├── connect
│   │       ├── disconnect
│   │       ├── subscribe
│   │       └── unsubscribe
│   └── daemon
├── dmsg
│   ├── conf
│   │   ├── gen-keys
│   │   └── verify-keys
│   ├── curl
│   ├── disc
│   ├── http
│   ├── ip
│   ├── pty
│   │   ├── cli
│   │   │   ├── whitelist
│   │   │   ├── whitelist-add
│   │   │   └── whitelist-remove
│   │   ├── host
│   │   │   └── confgen
│   │   └── ui
│   ├── self-ping
│   ├── server
│   │   ├── config
│   │   │   └── gen
│   │   ├── dial
│   │   └── start
│   ├── socks
│   │   ├── client
│   │   └── server
│   └── web
│       └── srv
├── skycoin
│   ├── cli
│   │   ├── addPrivateKey
│   │   ├── addressBalance
│   │   ├── addressGen
│   │   ├── addressOutputs
│   │   ├── addressTransactions
│   │   ├── addresscount
│   │   ├── blocks
│   │   ├── broadcastTransaction
│   │   ├── checkDBDecoding
│   │   ├── checkdb
│   │   ├── createRawTransaction
│   │   ├── createRawTransactionV2
│   │   ├── decodeRawTransaction
│   │   ├── decryptWallet
│   │   ├── distributeGenesis
│   │   ├── encodeJsonTransaction
│   │   ├── encryptWallet
│   │   ├── fiberAddressGen
│   │   ├── halt
│   │   ├── lastBlocks
│   │   ├── listAddresses
│   │   ├── listWallets
│   │   ├── nextAddress
│   │   ├── pendingTransactions
│   │   ├── richlist
│   │   ├── send
│   │   ├── showConfig
│   │   ├── showSeed
│   │   ├── signTransaction
│   │   ├── status
│   │   ├── transaction
│   │   ├── unusedAddresses
│   │   ├── verifyAddress
│   │   ├── verifyTransaction
│   │   ├── verifyXpub
│   │   ├── version
│   │   ├── walletAddAddresses
│   │   ├── walletBalance
│   │   ├── walletCreate
│   │   ├── walletCreateTemp
│   │   ├── walletHistory
│   │   ├── walletKeyExport
│   │   ├── walletOutputs
│   │   └── walletScanAddresses
│   ├── daemon
│   ├── explorer
│   ├── newcoin
│   │   ├── config
│   │   ├── createcoin
│   │   └── templates
│   └── web
├── svc
│   ├── ar
│   ├── conf
│   │   ├── dmsghttp
│   │   └── http
│   ├── confbs
│   ├── ip
│   ├── nm
│   │   └── deregister
│   ├── rf
│   ├── sd
│   ├── se
│   │   ├── dmsg
│   │   ├── setup
│   │   └── visor
│   ├── sn
│   │   └── health
│   ├── stun
│   ├── tpd
│   ├── tps
│   │   ├── add
│   │   ├── list
│   │   └── rm
│   └── ut
└── visor
```

## Skycoin Commands

go	go1.26.1
path	github.com/skycoin/skywire
mod	github.com/skycoin/skywire	v1.3.46-0.20260418190307-6747d8d73adf	
dep	atomicgo.dev/cursor	v0.2.0	
dep	atomicgo.dev/keyboard	v0.2.9	
dep	atomicgo.dev/schedule	v0.1.0	
dep	fyne.io/systray	v1.12.0	
dep	github.com/AudriusButkevicius/pfilter	v0.0.11	
dep	github.com/BurntSushi/toml	v1.6.0	
dep	github.com/DiSiqueira/GoTree	v1.0.0	
dep	github.com/MichaelMure/go-term-markdown	v0.1.4	
dep	github.com/MichaelMure/go-term-text	v0.3.1	
dep	github.com/NYTimes/gziphandler	v1.1.1	
dep	github.com/VictoriaMetrics/metrics	v1.43.2	
dep	github.com/VividCortex/ewma	v1.2.0	
dep	github.com/alecthomas/chroma	v0.10.0	
dep	github.com/anatol/smart.go	v0.0.0-20260314002218-4abf60ecc43c	
dep	github.com/armon/go-socks5	v0.0.0-20160902184237-e75332964ef5	
dep	github.com/atotto/clipboard	v0.1.4	
dep	github.com/aymanbagabas/go-osc52/v2	v2.0.1	
dep	github.com/bitfield/script	v0.24.1	
dep	github.com/blang/semver	v3.5.1+incompatible	
dep	github.com/blang/semver/v4	v4.0.0	
dep	github.com/ccding/go-stun	v0.1.5	
dep	github.com/cenkalti/backoff	v2.2.1+incompatible	
dep	github.com/cespare/xxhash/v2	v2.3.0	
dep	github.com/charmbracelet/bubbles	v1.0.0	
dep	github.com/charmbracelet/bubbletea	v1.3.10	
dep	github.com/charmbracelet/colorprofile	v0.4.3	
dep	github.com/charmbracelet/lipgloss	v1.1.0	
dep	github.com/charmbracelet/x/ansi	v0.11.7	
dep	github.com/charmbracelet/x/cellbuf	v0.0.15	
dep	github.com/charmbracelet/x/term	v0.2.2	
dep	github.com/chen3feng/safecast	v0.0.0-20220908170618-81b2ecd47937	
dep	github.com/clipperhouse/displaywidth	v0.11.0	
dep	github.com/clipperhouse/uax29/v2	v2.7.0	
dep	github.com/cloudfoundry-attic/jibber_jabber	v0.0.0-20151120183258-bcc4c8345a21	
dep	github.com/coder/websocket	v1.8.14	
dep	github.com/confiant-inc/go-socks5	v0.0.0-20210816151940-c1124825b1d6	
dep	github.com/containerd/console	v1.0.5	
dep	github.com/creack/pty	v1.1.24	
dep	github.com/davecgh/go-spew	v1.1.2-0.20180830191138-d8f796af33cc	
dep	github.com/dgryski/go-rendezvous	v0.0.0-20200823014737-9f7001d12a5f	
dep	github.com/disintegration/imaging	v1.6.2	
dep	github.com/distatus/battery	v0.10.0	
dep	github.com/dlclark/regexp2	v1.11.5	
dep	github.com/droundy/goopt	v0.0.0-20220217183150-48d6390ad4d1	
dep	github.com/elazarl/goproxy	v1.8.3	
dep	github.com/eliukblau/pixterm/pkg/ansimage	v0.0.0-20191210081756-9fb6cf8c2f75	
dep	github.com/fatih/color	v1.19.0	
dep	github.com/fsnotify/fsnotify	v1.9.0	
dep	github.com/gabriel-vasile/mimetype	v1.4.13	
dep	github.com/gdamore/encoding	v1.0.1	
dep	github.com/gdamore/tcell/v2	v2.13.8	
dep	github.com/gen2brain/dlgs	v0.0.0-20220603100644-40c77870fa8d	
dep	github.com/gin-contrib/sse	v1.1.1	
dep	github.com/gin-gonic/gin	v1.12.0	
dep	github.com/gizak/termui/v3	v3.1.0	
dep	github.com/go-chi/chi/v5	v5.2.5	
dep	github.com/go-chi/cors	v1.2.2	
dep	github.com/go-chi/httprate	v0.15.0	
dep	github.com/go-echarts/go-echarts/v2	v2.7.2	
dep	github.com/go-playground/locales	v0.14.1	
dep	github.com/go-playground/universal-translator	v0.18.1	
dep	github.com/go-playground/validator/v10	v10.30.2	
dep	github.com/go-redis/redis/v8	v8.11.5	
dep	github.com/go-viper/mapstructure/v2	v2.5.0	
dep	github.com/gocarina/gocsv	v0.0.0-20240520201108-78e41c74b4b1	
dep	github.com/goccy/go-yaml	v1.19.2	
dep	github.com/godbus/dbus/v5	v5.2.2	
dep	github.com/gomarkdown/markdown	v0.0.0-20260417124207-7d523f7318df	
dep	github.com/google/uuid	v1.6.0	
dep	github.com/gookit/color	v1.6.0	
dep	github.com/gorilla/securecookie	v1.1.2	
dep	github.com/hashicorp/go-version	v1.9.0	
dep	github.com/hashicorp/yamux	v0.1.2	
dep	github.com/itchyny/gojq	v0.12.19	
dep	github.com/itchyny/timefmt-go	v0.1.8	
dep	github.com/ivanpirog/coloredcobra	v1.0.1	
dep	github.com/jackc/pgpassfile	v1.0.0	
dep	github.com/jackc/pgservicefile	v0.0.0-20240606120523-5a60cdf6a761	
dep	github.com/jackc/pgx/v5	v5.9.1	
dep	github.com/jackc/puddle/v2	v2.2.2	
dep	github.com/james-barrow/golang-ipc	v1.2.4	
dep	github.com/jaypipes/ghw	v0.24.0	
dep	github.com/jaypipes/pcidb	v1.1.1	
dep	github.com/jinzhu/inflection	v1.0.0	
dep	github.com/jinzhu/now	v1.1.5	
dep	github.com/json-iterator/go	v1.1.12	
dep	github.com/klauspost/cpuid/v2	v2.3.0	
dep	github.com/klauspost/reedsolomon	v1.13.3	
dep	github.com/kyokomi/emoji/v2	v2.2.13	
dep	github.com/leodido/go-urn	v1.4.0	
dep	github.com/lib/pq	v1.12.3	
dep	github.com/lithammer/fuzzysearch	v1.1.8	
dep	github.com/lucasb-eyer/go-colorful	v1.4.0	
dep	github.com/mattn/go-colorable	v0.1.14	
dep	github.com/mattn/go-isatty	v0.0.21	
dep	github.com/mattn/go-runewidth	v0.0.23	
dep	github.com/mgutz/ansi	v0.0.0-20200706080929-d51e80ef957d	
dep	github.com/mitchellh/go-wordwrap	v1.0.1	
dep	github.com/modern-go/concurrent	v0.0.0-20180306012644-bacd9c7ef1dd	
dep	github.com/modern-go/reflect2	v1.0.2	
dep	github.com/muesli/ansi	v0.0.0-20230316100256-276c6243b2f6	
dep	github.com/muesli/cancelreader	v0.2.2	
dep	github.com/muesli/termenv	v0.16.0	
dep	github.com/nsf/termbox-go	v1.1.1	
dep	github.com/orandin/lumberjackrus	v1.0.1	
dep	github.com/oschwald/geoip2-golang/v2	v2.1.0	
dep	github.com/oschwald/maxminddb-golang/v2	v2.1.1	
dep	github.com/pelletier/go-toml/v2	v2.3.0	
dep	github.com/peterh/liner	v1.2.2	
dep	github.com/pgavlin/femto	v0.0.0-20201224065653-0c9d20f9cac4	
dep	github.com/pires/go-proxyproto	v0.12.0	
dep	github.com/pkg/errors	v0.9.1	
dep	github.com/pmezard/go-difflib	v1.0.1-0.20181226105442-5d4384ee4fb2	
dep	github.com/pterm/pterm	v0.12.83	
dep	github.com/quic-go/qpack	v0.6.0	
dep	github.com/quic-go/quic-go	v0.59.0	
dep	github.com/rivo/tview	v0.42.0	
dep	github.com/rivo/uniseg	v0.4.7	
dep	github.com/robert-nix/ansihtml	v1.0.1	
dep	github.com/rs/cors	v1.11.1	
dep	github.com/sagikazarmark/locafero	v0.12.0	
dep	github.com/sergi/go-diff	v1.4.0	
dep	github.com/shibukawa/configdir	v0.0.0-20170330084843-e180dbdc8da0	
dep	github.com/shirou/gopsutil/v3	v3.24.5	
dep	github.com/shopspring/decimal	v1.4.0	
dep	github.com/sirupsen/logrus	v1.9.4	
dep	github.com/skycoin/noise	v0.0.0-20180327030543-2492fe189ae6	
dep	github.com/skycoin/skycoin	v0.28.6-0.20260401142608-a27afbb0b33b	
dep	github.com/soheilhy/cmux	v0.1.5	
dep	github.com/songgao/water	v0.0.0-20200317203138-2b4b6d7c09d8	
dep	github.com/spf13/afero	v1.15.0	
dep	github.com/spf13/cast	v1.10.0	
dep	github.com/spf13/cobra	v1.10.2	
dep	github.com/spf13/pflag	v1.0.10	
dep	github.com/spf13/viper	v1.21.0	
dep	github.com/stretchr/objx	v0.5.3	
dep	github.com/stretchr/testify	v1.11.1	
dep	github.com/subosito/gotenv	v1.6.0	
dep	github.com/syndtr/gocapability	v0.0.0-20200815063812-42c35b437635	
dep	github.com/templexxx/cpufeat	v0.0.0-20180724012125-cef66df7f161	
dep	github.com/templexxx/xor	v0.0.0-20191217153810-f85b25db303b	
dep	github.com/tidwall/pretty	v1.2.1	
dep	github.com/tjfoc/gmsm	v1.4.1	
dep	github.com/tklauser/go-sysconf	v0.3.16	
dep	github.com/tklauser/numcpus	v0.11.0	
dep	github.com/toqueteos/webbrowser	v1.2.1	
dep	github.com/ugorji/go/codec	v1.3.1	
dep	github.com/valyala/fastrand	v1.1.0	
dep	github.com/valyala/histogram	v1.2.0	
dep	github.com/xo/terminfo	v0.0.0-20220910002029-abceb7e1c41e	
dep	github.com/xtaci/kcp-go	v5.4.20+incompatible	
dep	github.com/xtaci/smux	v1.5.57	
dep	github.com/xxxserxxx/gotop/v4	v4.2.1-0.20250927202203-54213c890e66	
dep	github.com/xxxserxxx/lingo/v2	v2.0.1	
dep	github.com/yuin/goldmark	v1.8.2	
dep	github.com/zcalusic/sysinfo	v1.1.3	
dep	github.com/zeebo/xxh3	v1.1.0	
dep	github.com/zyedidia/micro	v1.4.1	
dep	go.etcd.io/bbolt	v1.4.3	
dep	go.mongodb.org/mongo-driver/v2	v2.5.1	
dep	go.yaml.in/yaml/v3	v3.0.4	
dep	golang.org/x/crypto	v0.50.0	
dep	golang.org/x/image	v0.39.0	
dep	golang.org/x/net	v0.53.0	
dep	golang.org/x/sync	v0.20.0	
dep	golang.org/x/sys	v0.43.0	
dep	golang.org/x/term	v0.42.0	
dep	golang.org/x/text	v0.36.0	
dep	golang.org/x/time	v0.15.0	
dep	google.golang.org/genproto/googleapis/rpc	v0.0.0-20260414002931-afd174a4e478	
dep	google.golang.org/grpc	v1.80.0	
dep	google.golang.org/protobuf	v1.36.11	
dep	gopkg.in/natefinch/lumberjack.v2	v2.2.1	
dep	gopkg.in/telebot.v3	v3.3.8	
dep	gopkg.in/yaml.v2	v2.4.0	
dep	gopkg.in/yaml.v3	v3.0.1	
dep	gorm.io/driver/postgres	v1.6.0	
dep	gorm.io/gorm	v1.31.1	
dep	mvdan.cc/sh/v3	v3.13.1	
build	-buildmode=exe
build	-compiler=gc
build	CGO_ENABLED=1
build	CGO_CFLAGS=
build	CGO_CPPFLAGS=
build	CGO_CXXFLAGS=
build	CGO_LDFLAGS=
build	GOARCH=amd64
build	GOOS=linux
build	GOAMD64=v1
build	vcs=git
build	vcs.revision=6747d8d73adf948385701829e8f493bc854f6bad
build	vcs.time=2026-04-18T19:03:07Z
build	vcs.modified=false

