package http

import (
	"embed"
	"fmt"
	"io/fs"
	"log"
	"net/http"

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

//NewServer HTTP Server constructor
func NewServer(appServices app.Services) *Server {
	httpServer := &Server{appServices: appServices}
	httpServer.router = mux.NewRouter()
	httpServer.router.Handle("/", http.FileServer(getFileSystem()))
	//TODO: add router to favicon.ico instead of html base64 string
	//TODO: could not get it to work with go embed but it should work withit
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
	httpServer.router.HandleFunc(chatsHTTPRoutePath, chat.NewHandler(httpServer.appServices.ChatServices).GetAll).Methods("GET")
	httpServer.router.HandleFunc(chatsHTTPRoutePath+"/{"+chat.GetChatPKURLParam+"}", chat.NewHandler(httpServer.appServices.ChatServices).GetByPK).Methods("GET")

	//Commands
	httpServer.router.HandleFunc(chatsHTTPRoutePath, chat.NewHandler(httpServer.appServices.ChatServices).Add).Methods("POST")
	httpServer.router.HandleFunc(chatsHTTPRoutePath+"/sendmessage", chat.NewHandler(httpServer.appServices.ChatServices).SendTextMessage).Methods("POST")
	httpServer.router.HandleFunc(chatsHTTPRoutePath+"/{"+chat.DeleteChatPKURLParam+"}", chat.NewHandler(httpServer.appServices.ChatServices).Delete).Methods("DELETE")

}

// AddUserHTTPRoutes registers user route handlers
func (httpServer *Server) AddUserHTTPRoutes() {
	const userHTTPRoutePath = "/user"
	//Queries
	httpServer.router.HandleFunc(userHTTPRoutePath+"/"+user.GetInfoURLParam, user.NewHandler(httpServer.appServices.UserServices).GetInfo).Methods("GET")
	httpServer.router.HandleFunc(userHTTPRoutePath+"/"+user.GetSettingsURLParam, user.NewHandler(httpServer.appServices.UserServices).GetSettings).Methods("GET")

	//Commands
	httpServer.router.HandleFunc(userHTTPRoutePath+"/"+user.SetInfoURLParam, user.NewHandler(httpServer.appServices.UserServices).SetInfo).Methods("PUT")
	httpServer.router.HandleFunc(userHTTPRoutePath+"/"+user.SetSettingsURLParam, user.NewHandler(httpServer.appServices.UserServices).SetSettings).Methods("PUT")
}

// AddNotificationHTTPRoutes adds the sse route
func (httpServer *Server) AddNotificationHTTPRoutes() {
	const notificationHTTPRoutePath = "/notifications"
	//
	httpServer.router.HandleFunc(notificationHTTPRoutePath, notification.NewHandler(httpServer.appServices.NotificationService).SubscribeNotifications).Methods("GET")
}

//ListenAndServe Starts listening for requests
func (httpServer *Server) ListenAndServe(addr *string) {
	fmt.Println("Serving HTTP on", *addr)
	log.Fatal(http.ListenAndServe(*addr, nil))

}

//get index file
func getFileSystem() http.FileSystem {
	fsys, err := fs.Sub(embededFiles, "static")
	if err != nil {
		panic(err)
	}

	return http.FS(fsys)
}
