module github.com/skycoin/skywire

go 1.16

require (
	github.com/AudriusButkevicius/pfilter v0.0.0-20210218141631-7468b85d810a
	github.com/StackExchange/wmi v0.0.0-20210224194228-fe8f1750fd46 // indirect
	github.com/VictoriaMetrics/metrics v1.17.2
	github.com/armon/go-socks5 v0.0.0-20160902184237-e75332964ef5
	github.com/creack/pty v1.1.11 // indirect
	github.com/go-chi/chi v4.1.2+incompatible
	github.com/go-ole/go-ole v1.2.5 // indirect
	github.com/golang/protobuf v1.5.2 // indirect
	github.com/google/go-github v17.0.0+incompatible
	github.com/google/go-querystring v1.1.0 // indirect
	github.com/google/uuid v1.2.0
	github.com/gorilla/securecookie v1.1.1
	github.com/klauspost/reedsolomon v1.9.12 // indirect
	github.com/mattn/go-colorable v0.1.8 // indirect
	github.com/mgutz/ansi v0.0.0-20200706080929-d51e80ef957d // indirect
	github.com/mholt/archiver/v3 v3.5.0
	github.com/pkg/errors v0.9.1 // indirect
	github.com/pkg/profile v1.5.0
	github.com/schollz/progressbar/v2 v2.15.0
	github.com/shirou/gopsutil v3.21.3+incompatible
	github.com/sirupsen/logrus v1.8.1
	github.com/skycoin/dmsg v0.0.0-20210329160412-4e25fc9ad26c
	github.com/skycoin/skycoin v0.27.1
	github.com/skycoin/yamux v0.0.0-20200803175205-571ceb89da9f
	github.com/songgao/water v0.0.0-20200317203138-2b4b6d7c09d8
	github.com/spf13/cobra v1.1.3
	github.com/stretchr/testify v1.7.0
	github.com/syndtr/gocapability v0.0.0-20200815063812-42c35b437635
	github.com/templexxx/cpufeat v0.0.0-20180724012125-cef66df7f161 // indirect
	github.com/templexxx/xor v0.0.0-20191217153810-f85b25db303b // indirect
	github.com/tjfoc/gmsm v1.4.0 // indirect
	github.com/tklauser/go-sysconf v0.3.5 // indirect
	github.com/toqueteos/webbrowser v1.2.0
	github.com/xtaci/kcp-go v5.4.20+incompatible
	github.com/xtaci/lossyconn v0.0.0-20200209145036-adba10fffc37 // indirect
	go.etcd.io/bbolt v1.3.5
	golang.org/x/crypto v0.0.0-20210415154028-4f45737414dc // indirect
	golang.org/x/net v0.0.0-20210420210106-798c2154c571
	golang.org/x/sys v0.0.0-20210420205809-ac73e9fd8988
	golang.org/x/term v0.0.0-20210406210042-72f3dc4e9b72 // indirect
	golang.org/x/xerrors v0.0.0-20200804184101-5ec99f83aff1 // indirect
	golang.zx2c4.com/wireguard v0.0.20200320
	nhooyr.io/websocket v1.8.7
)

// Uncomment for tests with alternate branches of 'dmsg'
//replace github.com/skycoin/dmsg => ../dmsg
