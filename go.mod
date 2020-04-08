module github.com/SkycoinProject/skywire-mainnet

go 1.13

require (
	github.com/SkycoinProject/dmsg v0.0.0-20200306152741-acee74fa4514
	github.com/SkycoinProject/skycoin v0.27.0
	github.com/SkycoinProject/yamux v0.0.0-20191213015001-a36efeefbf6a
	github.com/armon/go-socks5 v0.0.0-20160902184237-e75332964ef5
	github.com/go-chi/chi v4.0.2+incompatible
	github.com/google/uuid v1.1.1
	github.com/gorilla/securecookie v1.1.1
	github.com/mholt/archiver/v3 v3.3.0
	github.com/pkg/profile v1.3.0
	github.com/prometheus/client_golang v1.3.0
	github.com/prometheus/common v0.7.0
	github.com/schollz/progressbar/v2 v2.15.0
	github.com/sirupsen/logrus v1.4.2
	github.com/skycoin/dmsg v0.0.0-20190805065636-70f4c32a994f // indirect
	github.com/songgao/water v0.0.0-20200317203138-2b4b6d7c09d8
	github.com/spf13/cobra v0.0.5
	github.com/stretchr/testify v1.4.0
	go.etcd.io/bbolt v1.3.4
	golang.org/x/net v0.0.0-20191204025024-5ee1b9f4859a
)

// Uncomment for tests with alternate branches of 'dmsg'
//replace github.com/SkycoinProject/dmsg => ../dmsg
