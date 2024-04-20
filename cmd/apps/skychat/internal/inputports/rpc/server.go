// Package rpc contains code of the rpc handler for inputports
package rpc

import (
	"net/http"
	"net/rpc"
	"time"

	"github.com/gorilla/mux"

	"github.com/skycoin/skycoin/src/util/logging"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/app"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/inputports/rpc/chat"
)

// Server represents the rpc server running for this service
type Server struct {
	appServices app.Services
	router      *mux.Router
	rpcPort     string
	log         *logging.Logger
}

// NewServer RPC Server constructor
func NewServer(appServices app.Services, rpcPort string) *Server {
	rpcServer := &Server{appServices: appServices, rpcPort: rpcPort, log: logging.MustGetLogger("chat:rpc-server")}
	rpcServer.router = mux.NewRouter()

	return rpcServer
}

// ListenAndServe Starts listening for requests
func (rpcServer *Server) ListenAndServe() {

	api := chat.NewHandler(rpcServer.appServices.ChatServices)
	err := rpc.Register(api)
	if err != nil {
		rpcServer.log.Fatal("error registering API", err)
	}

	rpc.HandleHTTP()

	rpcServer.log.Infoln("Serving RPC on", rpcServer.rpcPort)

	srv := &http.Server{
		Addr:         rpcServer.rpcPort,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	rpcServer.log.Fatal(srv.ListenAndServe())
}
