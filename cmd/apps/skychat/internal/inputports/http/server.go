// Package http is the server handler for inputports
package http

import (
	"embed"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"time"

	"github.com/gorilla/mux"

	"github.com/skycoin/skywire/cmd/apps/skychat/internal/app"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/inputports/http/chat"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/inputports/http/notification.go"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/inputports/http/user"
)

// the go embed static points to skywire/cmd/apps/skychat/internal/inputports/http/static

//go:embed static/*
var embededFiles embed.FS

// Server Represents the http server running for this service
type Server struct {
	appServices app.Services
	router      *mux.Router
}

// NewServer HTTP Server constructor
func NewServer(appServices app.Services) *Server {
	httpServer := &Server{appServices: appServices}
	httpServer.router = mux.NewRouter()
	httpServer.router.Handle("/", http.FileServer(getFileSystem()))
	httpServer.router.Handle("/favicon.ico", http.FileServer(getFileSystem()))
	httpServer.router.Handle("/index.js", http.FileServer(getFileSystem()))
	httpServer.router.Handle("/stylesheet.css", http.FileServer(getFileSystem()))

	httpServer.AddChatHTTPRoutes()
	httpServer.AddUserHTTPRoutes()
	httpServer.AddNotificationHTTPRoutes()
	http.Handle("/", httpServer.router)

	return httpServer
}

// AddChatHTTPRoutes registers chat route handlers
func (httpServer *Server) AddChatHTTPRoutes() {
	const chatsHTTPRoutePath = "/chats"
	//Queries
	httpServer.router.HandleFunc(chatsHTTPRoutePath+"/"+chat.GetAllMessagesFromRoomByRouteURLParam, chat.NewHandler(httpServer.appServices.ChatServices).GetAllMessagesFromRoomByRoute).Methods("GET")
	httpServer.router.HandleFunc(chatsHTTPRoutePath, chat.NewHandler(httpServer.appServices.ChatServices).GetAllVisors).Methods("GET")
	httpServer.router.HandleFunc(chatsHTTPRoutePath+"/"+chat.GetRoomByRouteURLParam, chat.NewHandler(httpServer.appServices.ChatServices).GetRoomByRoute).Methods("GET")
	httpServer.router.HandleFunc(chatsHTTPRoutePath+"/"+chat.GetServerByRouteURLParam, chat.NewHandler(httpServer.appServices.ChatServices).GetServerByRoute).Methods("GET")
	httpServer.router.HandleFunc(chatsHTTPRoutePath+"/{"+chat.GetVisorByPKURLParam+"}", chat.NewHandler(httpServer.appServices.ChatServices).GetVisorByPK).Methods("GET")

	//Commands
	httpServer.router.HandleFunc(chatsHTTPRoutePath+"/"+chat.AddLocalServerURLParam, chat.NewHandler(httpServer.appServices.ChatServices).AddLocalServer).Methods("POST")
	httpServer.router.HandleFunc(chatsHTTPRoutePath+"/"+chat.JoinRemoteRouteURLParam, chat.NewHandler(httpServer.appServices.ChatServices).JoinRemoteRoute).Methods("POST")
	httpServer.router.HandleFunc(chatsHTTPRoutePath+"/{"+chat.DeleteVisorByPKURLParam+"}", chat.NewHandler(httpServer.appServices.ChatServices).DeleteVisorByPK).Methods("DELETE")
	httpServer.router.HandleFunc(chatsHTTPRoutePath+"/"+chat.LeaveRemoteRouteURLParam, chat.NewHandler(httpServer.appServices.ChatServices).LeaveRemoteRoute).Methods("POST")
	httpServer.router.HandleFunc(chatsHTTPRoutePath+"/"+chat.SendAddRoomMessageURLParam, chat.NewHandler(httpServer.appServices.ChatServices).SendAddRoomMessage).Methods("POST")
	httpServer.router.HandleFunc(chatsHTTPRoutePath+"/"+chat.SendDeleteRoomMessageURLParam, chat.NewHandler(httpServer.appServices.ChatServices).SendDeleteRoomMessage).Methods("POST")
	httpServer.router.HandleFunc(chatsHTTPRoutePath+"/"+chat.SendTextMessagePKURLParam, chat.NewHandler(httpServer.appServices.ChatServices).SendTextMessage).Methods("POST")

}

// AddUserHTTPRoutes registers user route handlers
func (httpServer *Server) AddUserHTTPRoutes() {
	const userHTTPRoutePath = "/user"
	//Queries
	httpServer.router.HandleFunc(userHTTPRoutePath+"/"+user.GetInfoURLParam, user.NewHandler(httpServer.appServices.UserServices).GetInfo).Methods("GET")
	httpServer.router.HandleFunc(userHTTPRoutePath+"/"+user.GetSettingsURLParam, user.NewHandler(httpServer.appServices.UserServices).GetSettings).Methods("GET")
	httpServer.router.HandleFunc(userHTTPRoutePath+"/"+user.GetPeerbookURLParam, user.NewHandler(httpServer.appServices.UserServices).GetPeerbook).Methods("GET")

	//Commands
	httpServer.router.HandleFunc(userHTTPRoutePath+"/"+user.SetInfoURLParam, user.NewHandler(httpServer.appServices.UserServices).SetInfo).Methods("PUT")
	httpServer.router.HandleFunc(userHTTPRoutePath+"/"+user.SetSettingsURLParam, user.NewHandler(httpServer.appServices.UserServices).SetSettings).Methods("PUT")
	httpServer.router.HandleFunc(userHTTPRoutePath+"/"+user.AddPeerURLParam, user.NewHandler(httpServer.appServices.UserServices).AddPeer).Methods("PUT")
	httpServer.router.HandleFunc(userHTTPRoutePath+"/"+user.SetPeerURLParam, user.NewHandler(httpServer.appServices.UserServices).SetPeer).Methods("PUT")
	httpServer.router.HandleFunc(userHTTPRoutePath+"/{"+user.DeletePeerURLParam+"}", user.NewHandler(httpServer.appServices.UserServices).DeletePeer).Methods("DELETE")

}

// AddNotificationHTTPRoutes adds the sse route
func (httpServer *Server) AddNotificationHTTPRoutes() {
	const notificationHTTPRoutePath = "/notifications"
	//
	httpServer.router.HandleFunc(notificationHTTPRoutePath, notification.NewHandler(httpServer.appServices.NotificationService).SubscribeNotifications).Methods("GET")
}

// ListenAndServe Starts listening for requests
func (httpServer *Server) ListenAndServe(addr *string) {
	fmt.Println("Serving HTTP on", *addr)
	srv := &http.Server{
		Addr:         *addr,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	log.Fatal(srv.ListenAndServe())

}

// getFileSystem gets index file
func getFileSystem() http.FileSystem {
	fsys, err := fs.Sub(embededFiles, "static")
	if err != nil {
		panic(err)
	}

	return http.FS(fsys)
}
