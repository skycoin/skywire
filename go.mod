module github.com/SkycoinProject/skywire-mainnet

go 1.13

require (
	github.com/SkycoinProject/dmsg v0.0.0-20200116114634-91be578a1895
	github.com/SkycoinProject/skycoin v0.27.0
	github.com/SkycoinProject/skywire-peering-daemon v0.0.0-20200127113205-a3b6ccb52180
	github.com/SkycoinProject/yamux v0.0.0-20191213015001-a36efeefbf6a
	github.com/armon/go-socks5 v0.0.0-20160902184237-e75332964ef5
	github.com/creack/pty v1.1.9
	github.com/go-chi/chi v4.0.2+incompatible
	github.com/google/uuid v1.1.1
	github.com/gorilla/handlers v1.4.2
	github.com/gorilla/securecookie v1.1.1
	github.com/hashicorp/yamux v0.0.0-20190923154419-df201c70410d
	github.com/konsorten/go-windows-terminal-sequences v1.0.2 // indirect
	github.com/kr/pretty v0.1.0 // indirect
	github.com/pkg/profile v1.3.0
	github.com/prometheus/client_golang v1.3.0
	github.com/prometheus/common v0.7.0
	github.com/rjeczalik/notify v0.9.2
	github.com/sirupsen/logrus v1.4.2
	github.com/spf13/cobra v0.0.5
	github.com/stretchr/testify v1.4.0
	go.etcd.io/bbolt v1.3.3
	golang.org/x/crypto v0.0.0-20191227163750-53104e6ec876
	golang.org/x/net v0.0.0-20191204025024-5ee1b9f4859a
	golang.org/x/text v0.3.2 // indirect
	golang.org/x/tools v0.0.0-20200203023011-6f24f261dadb // indirect
)

replace (
	github.com/SkycoinProject/dmsg => ../dmsg
	github.com/SkycoinProject/skywire-peering-daemon => ../skywire-peering-daemon
)
