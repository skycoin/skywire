module github.com/skycoin/skywire

go 1.14

require (
	github.com/AudriusButkevicius/pfilter v0.0.0-20190627213056-c55ef6137fc6
	github.com/StackExchange/wmi v0.0.0-20190523213315-cbe66965904d // indirect
	github.com/armon/go-socks5 v0.0.0-20160902184237-e75332964ef5
	github.com/go-chi/chi v4.1.2+incompatible
	github.com/go-ole/go-ole v1.2.4 // indirect
	github.com/google/go-github v17.0.0+incompatible
	github.com/google/go-querystring v1.0.0 // indirect
	github.com/google/uuid v1.1.1
	github.com/gorilla/securecookie v1.1.1
	github.com/klauspost/reedsolomon v1.9.9 // indirect
	github.com/mholt/archiver/v3 v3.3.0
	github.com/mmcloughlin/avo v0.0.0-20200523190732-4439b6b2c061 // indirect
	github.com/pkg/profile v1.5.0
	github.com/prometheus/client_golang v1.7.1
	github.com/rakyll/statik v0.1.7
	github.com/schollz/progressbar/v2 v2.15.0
	github.com/shirou/gopsutil v2.20.5+incompatible
	github.com/sirupsen/logrus v1.6.0
	github.com/skycoin/dmsg v0.0.0-20201009105839-3b0627b4ffa6
	github.com/skycoin/skycoin v0.26.0
	github.com/skycoin/yamux v0.0.0-20200803175205-571ceb89da9f
	github.com/songgao/water v0.0.0-20200317203138-2b4b6d7c09d8
	github.com/spf13/cobra v1.0.0
	github.com/stretchr/testify v1.6.1
	github.com/templexxx/cpufeat v0.0.0-20180724012125-cef66df7f161 // indirect
	github.com/templexxx/xor v0.0.0-20191217153810-f85b25db303b // indirect
	github.com/tjfoc/gmsm v1.3.2 // indirect
	github.com/xtaci/kcp-go v5.4.20+incompatible
	github.com/xtaci/lossyconn v0.0.0-20200209145036-adba10fffc37 // indirect
	go.etcd.io/bbolt v1.3.5
	golang.org/x/net v0.0.0-20200625001655-4c5254603344
	golang.zx2c4.com/wireguard v0.0.20200320
	nhooyr.io/websocket v1.8.2
)

// Uncomment for tests with alternate branches of 'dmsg'
//replace github.com/skycoin/dmsg => ../dmsg
