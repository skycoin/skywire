module github.com/skycoin/skywire

go 1.17

require (
	github.com/AudriusButkevicius/pfilter v0.0.0-20210515103320-4b4b86609d51
	github.com/VictoriaMetrics/metrics v1.18.1
	github.com/armon/go-socks5 v0.0.0-20160902184237-e75332964ef5
	github.com/ccding/go-stun/stun v0.0.0-20200514191101-4dc67bcdb029
	github.com/gen2brain/dlgs v0.0.0-20210911090025-cbd38e821b98
	github.com/google/uuid v1.1.2
	github.com/gorilla/securecookie v1.1.1
	github.com/json-iterator/go v1.1.12 // indirect
	github.com/klauspost/reedsolomon v1.9.9 // indirect
	github.com/konsorten/go-windows-terminal-sequences v1.0.2
	github.com/mattn/go-colorable v0.1.12 // indirect
	github.com/mgutz/ansi v0.0.0-20200706080929-d51e80ef957d // indirect
	github.com/mmcloughlin/avo v0.0.0-20200523190732-4439b6b2c061 // indirect
	github.com/pkg/profile v1.5.0
	github.com/shirou/gopsutil/v3 v3.21.4
	github.com/sirupsen/logrus v1.8.1
	github.com/skycoin/skycoin v0.27.1
	github.com/skycoin/yamux v0.0.0-20200803175205-571ceb89da9f
	github.com/songgao/water v0.0.0-20200317203138-2b4b6d7c09d8
	github.com/spf13/cobra v1.4.0
	github.com/stretchr/testify v1.7.0
	github.com/syndtr/gocapability v0.0.0-20200815063812-42c35b437635
	github.com/templexxx/cpufeat v0.0.0-20180724012125-cef66df7f161 // indirect
	github.com/templexxx/xor v0.0.0-20191217153810-f85b25db303b // indirect
	github.com/tjfoc/gmsm v1.4.0 // indirect
	github.com/toqueteos/webbrowser v1.2.0
	github.com/xtaci/kcp-go v5.4.20+incompatible
	go.etcd.io/bbolt v1.3.6
	golang.org/x/net v0.0.0-20211020060615-d418f374d309
	golang.org/x/sys v0.0.0-20220627191245-f75cf1eec38b
	golang.org/x/term v0.0.0-20210927222741-03fcf44c2211 // indirect
	golang.org/x/tools v0.1.5 // indirect
	golang.zx2c4.com/wireguard v0.0.0-20211012180210-dfd688b6aa7b
	nhooyr.io/websocket v1.8.2 // indirect
)

require (
	github.com/bitfield/script v0.19.0
	github.com/blang/semver/v4 v4.0.0
	github.com/go-chi/chi/v5 v5.0.8-0.20220103230436-7dbe9a0bd10f
	github.com/ivanpirog/coloredcobra v1.0.0
	github.com/james-barrow/golang-ipc v0.0.0-20210227130457-95e7cc81f5e2
	github.com/skycoin/dmsg v0.0.0-20220904231115-c313c992c788
	github.com/skycoin/skywire-utilities v0.0.0-20220712142443-abafa30105ce
	github.com/skycoin/systray v1.10.1-0.20220630135132-48d2a1fb85d8
	github.com/spf13/pflag v1.0.5
	periph.io/x/periph v3.6.8+incompatible
)

require (
	bitbucket.org/creachadair/shell v0.0.7 // indirect
	github.com/ActiveState/termtest/conpty v0.5.0 // indirect
	github.com/Azure/go-ansiterm v0.0.0-20170929234023-d6e3b3328b78 // indirect
	github.com/Microsoft/go-winio v0.4.16 // indirect
	github.com/StackExchange/wmi v0.0.0-20190523213315-cbe66965904d // indirect
	github.com/creack/pty v1.1.15 // indirect
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/fatih/color v1.13.0 // indirect
	github.com/go-ole/go-ole v1.2.4 // indirect
	github.com/godbus/dbus/v5 v5.1.0 // indirect
	github.com/gopherjs/gopherjs v0.0.0-20181017120253-0766667cb4d1 // indirect
	github.com/inconshreveable/mousetrap v1.0.0 // indirect
	github.com/klauspost/compress v1.11.0 // indirect
	github.com/klauspost/cpuid v1.2.4 // indirect
	github.com/mattn/go-isatty v0.0.14 // indirect
	github.com/modern-go/concurrent v0.0.0-20180306012644-bacd9c7ef1dd // indirect
	github.com/modern-go/reflect2 v1.0.2 // indirect
	github.com/pkg/errors v0.9.1 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	github.com/skycoin/noise v0.0.0-20180327030543-2492fe189ae6 // indirect
	github.com/stretchr/objx v0.1.1 // indirect
	github.com/tevino/abool v1.2.0 // indirect
	github.com/tklauser/go-sysconf v0.3.4 // indirect
	github.com/tklauser/numcpus v0.2.1 // indirect
	github.com/valyala/fastrand v1.1.0 // indirect
	github.com/valyala/histogram v1.2.0 // indirect
	golang.org/x/crypto v0.0.0-20210921155107-089bfa567519 // indirect
	golang.org/x/mod v0.5.0 // indirect
	golang.org/x/xerrors v0.0.0-20200804184101-5ec99f83aff1 // indirect
	gopkg.in/yaml.v3 v3.0.0-20210107192922-496545a6307b // indirect
)

// Uncomment for tests with alternate branches of 'dmsg'
// replace github.com/skycoin/dmsg => ../dmsg

// Uncomment for tests with alternate branches of 'skywire-utilities'
// replace github.com/skycoin/skywire-utilities => ../skywire-utilities
