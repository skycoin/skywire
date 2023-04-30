// Package rpc contains code of the rpc handler for inputports
package rpc

import (
	"fmt"
	"log"
	"net/http"
	"net/rpc"
	"time"

	"github.com/gorilla/mux"

	"github.com/skycoin/skywire/cmd/apps/skychat/internal/app"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/inputports/rpc/chat"
)

// Server represents the rpc server running for this service
type Server struct {
	appServices app.Services
	router      *mux.Router
}

// NewServer RPC Server constructor
func NewServer(appServices app.Services) *Server {
	rpcServer := &Server{appServices: appServices}
	rpcServer.router = mux.NewRouter()

	return rpcServer
}

// ListenAndServe Starts listening for requests
func (rpcServer *Server) ListenAndServe(port *string) {

	api := chat.NewHandler(rpcServer.appServices.ChatServices)
	err := rpc.Register(api)
	if err != nil {
		log.Fatal("error registering API", err)
	}

	rpc.HandleHTTP()

	fmt.Println("Serving RPC on", *port)

	srv := &http.Server{
		Addr:         *port,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	log.Fatal(srv.ListenAndServe())
}
