module github.com/SkycoinProject/skywire-mainnet

go 1.13

require (
	github.com/SkycoinProject/dmsg v0.0.0-20200203035036-dbbed345b710
	github.com/SkycoinProject/skycoin v0.27.0
	github.com/SkycoinProject/yamux v0.0.0-20191213015001-a36efeefbf6a
	github.com/armon/go-socks5 v0.0.0-20160902184237-e75332964ef5
	github.com/creack/pty v1.1.9
	github.com/go-chi/chi v4.0.2+incompatible
	github.com/google/uuid v1.1.1
	github.com/gorilla/handlers v1.4.2
	github.com/gorilla/securecookie v1.1.1
	github.com/hashicorp/yamux v0.0.0-20190923154419-df201c70410d
	github.com/konsorten/go-windows-terminal-sequences v1.0.2 // indirect
	github.com/kr/pretty v0.2.0 // indirect
	github.com/mattn/go-isatty v0.0.12 // indirect
	github.com/pkg/profile v1.3.0
	github.com/prometheus/client_golang v1.3.0
	github.com/prometheus/common v0.7.0
	github.com/sirupsen/logrus v1.4.2
	github.com/spf13/cobra v0.0.5
	github.com/stretchr/testify v1.4.0
	go.etcd.io/bbolt v1.3.3
	golang.org/x/crypto v0.0.0-20200128174031-69ecbb4d6d5d
	golang.org/x/net v0.0.0-20191204025024-5ee1b9f4859a
	golang.org/x/sys v0.0.0-20200202164722-d101bd2416d5 // indirect
)

//replace github.com/SkycoinProject/dmsg => ../dmsg
