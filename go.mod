module github.com/skycoin/skywire

go 1.13

require (
	github.com/armon/go-socks5 v0.0.0-20160902184237-e75332964ef5
	github.com/go-chi/chi v4.0.2+incompatible
	github.com/google/uuid v1.1.1
	github.com/gorilla/securecookie v1.1.1
	github.com/mholt/archiver/v3 v3.3.0
	github.com/pkg/profile v1.3.0
	github.com/prometheus/client_golang v1.3.0
	github.com/prometheus/common v0.7.0
	github.com/rakyll/statik v0.1.7
	github.com/schollz/progressbar/v2 v2.15.0
	github.com/sirupsen/logrus v1.4.2
	github.com/skycoin/dmsg v0.0.0-20200807121748-518564603fc3
	github.com/skycoin/skycoin v0.26.0
	github.com/skycoin/yamux v0.0.0-20200803175205-571ceb89da9f
	github.com/spf13/cobra v0.0.5
	github.com/stretchr/testify v1.4.0
	go.etcd.io/bbolt v1.3.4
	golang.org/x/net v0.0.0-20191204025024-5ee1b9f4859a
)

// Uncomment for tests with alternate branches of 'dmsg'
//replace github.com/skycoin/dmsg => ../dmsg
