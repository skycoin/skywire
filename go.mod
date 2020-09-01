module github.com/skycoin/skywire

go 1.13

require (
	github.com/andybalholm/brotli v1.0.0 // indirect
	github.com/armon/go-socks5 v0.0.0-20160902184237-e75332964ef5
	github.com/creack/pty v1.1.11 // indirect
	github.com/frankban/quicktest v1.10.2 // indirect
	github.com/go-chi/chi v4.1.2+incompatible
	github.com/google/uuid v1.1.2
	github.com/gorilla/handlers v1.5.0 // indirect
	github.com/gorilla/securecookie v1.1.1
	github.com/klauspost/compress v1.10.11 // indirect
	github.com/klauspost/pgzip v1.2.4 // indirect
	github.com/mholt/archiver/v3 v3.3.0
	github.com/nwaples/rardecode v1.1.0 // indirect
	github.com/pierrec/lz4 v2.5.2+incompatible // indirect
	github.com/pkg/profile v1.3.0
	github.com/prometheus/client_golang v1.3.0
	github.com/prometheus/common v0.7.0
	github.com/rakyll/statik v0.1.7
	github.com/schollz/progressbar/v2 v2.15.0
	github.com/sirupsen/logrus v1.6.0
	github.com/skycoin/dmsg v0.0.0-20200831144948-62ac73c727f9
	github.com/skycoin/skycoin v0.26.0
	github.com/skycoin/yamux v0.0.0-20200803175205-571ceb89da9f
	github.com/spf13/cobra v1.0.0
	github.com/spf13/pflag v1.0.5 // indirect
	github.com/stretchr/objx v0.3.0 // indirect
	github.com/stretchr/testify v1.6.1
	github.com/ulikunitz/xz v0.5.8 // indirect
	go.etcd.io/bbolt v1.3.5
	golang.org/x/crypto v0.0.0-20200820211705-5c72a883971a // indirect
	golang.org/x/net v0.0.0-20200822124328-c89045814202
	golang.org/x/sys v0.0.0-20200831180312-196b9ba8737a // indirect
	gopkg.in/check.v1 v1.0.0-20190902080502-41f04d3bba15 // indirect
	gopkg.in/yaml.v3 v3.0.0-20200615113413-eeeca48fe776 // indirect
	nhooyr.io/websocket v1.8.6 // indirect
)

// Uncomment for tests with alternate branches of 'dmsg'
//replace github.com/skycoin/dmsg => ../dmsg
