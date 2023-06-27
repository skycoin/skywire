// Package dmsghttp_test pkg/dmsghttp/examples_test.go
package dmsghttp_test

import (
	"context"
	"fmt"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/sirupsen/logrus"
	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire-utilities/pkg/cmdutil"
	"github.com/skycoin/skywire-utilities/pkg/logging"
	"golang.org/x/net/nettest"

	"github.com/skycoin/skywire/pkg/disc"
	"github.com/skycoin/skywire/pkg/dmsg"
	"github.com/skycoin/skywire/pkg/dmsghttp"
)

func ExampleMakeHTTPTransport() {
	logrus.SetLevel(logrus.FatalLevel)

	const maxSessions = 100
	const dmsgHTTPPort = 80

	// Create a mock dmsg discovery.
	dc := disc.NewMock(0)

	// Create a dmsg server to relay all requests/responses.
	srvPK, srvSK := cipher.GenerateKeyPair()
	srvConf := dmsg.ServerConfig{
		MaxSessions:    maxSessions,
		UpdateInterval: 0,
	}
	srv := dmsg.NewServer(srvPK, srvSK, dc, &srvConf, nil)
	defer func() {
		if err := srv.Close(); err != nil {
			panic(err)
		}
	}()
	go func() {
		lis, err := nettest.NewLocalListener("tcp")
		if err != nil {
			panic(err)
		}
		if err := srv.Serve(lis, ""); err != nil {
			panic(err)
		}
	}()
	<-srv.Ready()

	// Create a dmsg client to host http server.
	c1PK, c1SK := cipher.GenerateKeyPair()
	dmsgC1 := dmsg.NewClient(c1PK, c1SK, dc, nil)
	defer func() {
		if err := dmsgC1.Close(); err != nil {
			panic(err)
		}
	}()
	go dmsgC1.Serve(context.Background())
	<-dmsgC1.Ready()

	// Host HTTP server via dmsg client 1.
	lis, err := dmsgC1.Listen(dmsgHTTPPort)
	if err != nil {
		panic(err)
	}
	defer func() {
		if err := lis.Close(); err != nil {
			panic(err)
		}
	}()

	r := chi.NewRouter()
	r.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("<html><body><h1>Hello World!</h1></body></html>")) //nolint:errcheck
	})
	go func() { _ = http.Serve(lis, r) }() //nolint
	// Create dmsg client to run http client.
	c2PK, c2SK := cipher.GenerateKeyPair()
	dmsgC2 := dmsg.NewClient(c2PK, c2SK, dc, nil)
	defer func() {
		if err := dmsgC2.Close(); err != nil {
			panic(err)
		}
	}()
	go dmsgC2.Serve(context.Background())
	<-dmsgC2.Ready()

	log := logging.MustGetLogger("http_client")
	ctx, cancel := cmdutil.SignalContext(context.Background(), log)
	defer cancel()
	// Run HTTP client.
	httpC := http.Client{Transport: dmsghttp.MakeHTTPTransport(ctx, dmsgC2)}
	resp, err := httpC.Get(fmt.Sprintf("http://%s:%d/", c1PK.String(), dmsgHTTPPort))
	if err != nil {
		panic(err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			panic(err)
		}
	}()
	readB, err := io.ReadAll(resp.Body)
	if err != nil {
		panic(err)
	}

	// Print read data.
	fmt.Println("READ:", string(readB))
	// Output: READ: <html><body><h1>Hello World!</h1></body></html>
}
