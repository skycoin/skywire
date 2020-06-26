module github.com/SkycoinProject/skywire-mainnet

go 1.14

require (
	github.com/SkycoinProject/dmsg v0.2.3-0.20200626050336-ed5aa457d4be
	github.com/SkycoinProject/skycoin v0.27.0
	github.com/SkycoinProject/yamux v0.0.0-20191213015001-a36efeefbf6a
	github.com/StackExchange/wmi v0.0.0-20190523213315-cbe66965904d // indirect
	github.com/armon/go-socks5 v0.0.0-20160902184237-e75332964ef5
	github.com/go-chi/chi v4.0.2+incompatible
	github.com/go-ole/go-ole v1.2.4 // indirect
	github.com/google/go-github v17.0.0+incompatible
	github.com/google/go-querystring v1.0.0 // indirect
	github.com/google/uuid v1.1.1
	github.com/gorilla/securecookie v1.1.1
	github.com/klauspost/reedsolomon v1.9.9 // indirect
	github.com/konsorten/go-windows-terminal-sequences v1.0.3 // indirect
	github.com/libp2p/go-reuseport v0.0.1
	github.com/mholt/archiver/v3 v3.3.0
	github.com/mmcloughlin/avo v0.0.0-20200523190732-4439b6b2c061 // indirect
	github.com/pkg/errors v0.9.1 // indirect
	github.com/pkg/profile v1.3.0
	github.com/prometheus/client_golang v1.3.0
	github.com/prometheus/common v0.7.0
	github.com/rakyll/statik v0.1.7
	github.com/schollz/progressbar/v2 v2.15.0
	github.com/shirou/gopsutil v2.20.5+incompatible
	github.com/sirupsen/logrus v1.5.0
	github.com/songgao/water v0.0.0-20200317203138-2b4b6d7c09d8
	github.com/spf13/cobra v0.0.5
	github.com/stretchr/testify v1.5.1
	github.com/templexxx/cpufeat v0.0.0-20180724012125-cef66df7f161 // indirect
	github.com/templexxx/xor v0.0.0-20191217153810-f85b25db303b // indirect
	github.com/tjfoc/gmsm v1.3.1 // indirect
	github.com/xtaci/kcp-go v4.3.4+incompatible
	go.etcd.io/bbolt v1.3.4
	golang.org/x/net v0.0.0-20200324143707-d3edc9973b7e
	golang.zx2c4.com/wireguard v0.0.20200320
	nhooyr.io/websocket v1.8.2
)

// Uncomment for tests with alternate branches of 'dmsg'
//replace github.com/SkycoinProject/dmsg => ../dmsg
