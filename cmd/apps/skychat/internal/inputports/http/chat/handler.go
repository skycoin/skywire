// Package chat is the http handler for inputports
package chat

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/gorilla/mux"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	chatservices "github.com/skycoin/skywire/cmd/apps/skychat/internal/app/chat"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/app/chat/commands"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/app/chat/queries"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/info"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/util"
)

// Handler Chat http request handler
type Handler struct {
	chatServices chatservices.ChatServices
}

// NewHandler Constructor returns *Handler
func NewHandler(cs chatservices.ChatServices) *Handler {
	return &Handler{chatServices: cs}
}

//[]: change to AddRoom -> as this also can be used to add a room at a remote server (when being admin)

// AddLocalRoomURLParam contains the parameter identifier to be parsed by the handler
const AddLocalRoomURLParam = "addLocalRoom"

// AddLocalRoomRequestModel represents the request model expected for Add request
type AddLocalRoomRequestModel struct {
	//PKRoute
	VisorPk  string `json:"visorpk"`
	ServerPk string `json:"serverpk"`
	//Info
	Alias string `json:"alias"`
	Desc  string `json:"desc"`
	Img   string `json:"img"`
	//Type
	Type string `json:"type"`
}

// AddLocalRoom adds a room to the local visor/server
func (c Handler) AddLocalRoom(w http.ResponseWriter, r *http.Request) {
	var requestModel AddLocalRoomRequestModel
	decodeErr := json.NewDecoder(r.Body).Decode(&requestModel)
	if decodeErr != nil {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, decodeErr.Error())
		return
	}

	route := util.PKRoute{}

	visorpk := cipher.PubKey{}
	err := visorpk.Set(requestModel.VisorPk)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, err.Error())
		return
	}
	route.Visor = visorpk
	if requestModel.ServerPk != "" {
		serverpk := cipher.PubKey{}
		err = serverpk.Set(requestModel.ServerPk)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, err.Error())
			return
		}
		route.Server = serverpk
	}

	info := info.Info{}
	info.SetAlias(requestModel.Alias)
	info.SetDescription(requestModel.Desc)
	info.SetImg(requestModel.Img)

	var roomType int64

	if requestModel.Type != "" {
		roomType, err = strconv.ParseInt(requestModel.Type, 10, 64)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, err.Error())
			return
		}
	} else {
		roomType = 1
	}

	err = c.chatServices.Commands.AddLocalRoomHandler.Handle(commands.AddLocalRoomRequest{
		Route: route,
		Info:  info,
		Type:  roomType,
	})
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, err.Error())
		return
	}
	w.WriteHeader(http.StatusOK)
}

//[]: change to AddServer -> as this also can be used to add a server at a remote visor (when being admin)

// AddLocalServerURLParam contains the parameter identifier to be parsed by the handler
const AddLocalServerURLParam = "addLocalServer"

// AddLocalServerRequestModel represents the request model expected for Add request
type AddLocalServerRequestModel struct {
	//PKRoute
	VisorPk string `json:"visorpk"`
	//Info
	Alias string `json:"alias"`
	Desc  string `json:"desc"`
	Img   string `json:"img"`
}

// AddLocalServer adds a room to the local visor/server
func (c Handler) AddLocalServer(w http.ResponseWriter, r *http.Request) {
	var requestModel AddLocalServerRequestModel
	decodeErr := json.NewDecoder(r.Body).Decode(&requestModel)
	if decodeErr != nil {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, decodeErr.Error())
		return
	}

	route := util.PKRoute{}

	visorpk := cipher.PubKey{}
	err := visorpk.Set(requestModel.VisorPk)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, err.Error())
		return
	}
	route.Visor = visorpk

	info := info.Info{}
	info.SetAlias(requestModel.Alias)
	info.SetDescription(requestModel.Desc)
	info.SetImg(requestModel.Img)

	err = c.chatServices.Commands.AddLocalServerHandler.Handle(commands.AddLocalServerRequest{
		Route: route,
		Info:  info,
	})
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, err.Error())
		return
	}
	w.WriteHeader(http.StatusOK)
}

// AddRemoteRouteURLParam contains the parameter identifier to be parsed by the handler
const AddRemoteRouteURLParam = "addRemoteRoute"

// AddRemoteRouteRequestModel represents the request model expected for Add request
type AddRemoteRouteRequestModel struct {
	VisorPk  string `json:"visorpk"`
	ServerPk string `json:"serverpk"`
	RoomPk   string `json:"roompk"`
}

// AddRemoteRoute adds the provided route
func (c Handler) AddRemoteRoute(w http.ResponseWriter, r *http.Request) {
	//fmt.Println(formatRequest(r))
	var routeToAdd AddRemoteRouteRequestModel
	decodeErr := json.NewDecoder(r.Body).Decode(&routeToAdd)
	if decodeErr != nil {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, decodeErr.Error())
		return
	}
	fmt.Println("HTTPHandler - Add - Route: " + routeToAdd.VisorPk + "," + routeToAdd.ServerPk + "," + routeToAdd.RoomPk)

	route := util.PKRoute{}

	visorpk := cipher.PubKey{}
	err := visorpk.Set(routeToAdd.VisorPk)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, err.Error())
		return
	}
	route.Visor = visorpk
	if routeToAdd.ServerPk != "" {
		serverpk := cipher.PubKey{}
		err = serverpk.Set(routeToAdd.ServerPk)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, err.Error())
			return
		}
		route.Server = serverpk
		if routeToAdd.RoomPk != "" {
			roompk := cipher.PubKey{}
			err = roompk.Set(routeToAdd.RoomPk)
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				fmt.Fprint(w, err.Error())
				return
			}
			route.Room = roompk
		}
	}

	err = c.chatServices.Commands.AddRemoteRouteHandler.Handle(commands.AddRemoteRouteRequest{
		Route: route,
	})
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, err.Error())
		return
	}
	w.WriteHeader(http.StatusOK)
}

// DeleteLocalRoomByRouteURLParam contains the parameter identifier to be parsed by the handler
const DeleteLocalRoomByRouteURLParam = "deleteLocalRoom"

// DeleteLocalRoomByRouteRequestModel represents the request model expected for Delete request
type DeleteLocalRoomByRouteRequestModel struct {
	VisorPk  string `json:"visorpk"`
	ServerPk string `json:"serverpk"`
	RoomPk   string `json:"roompk"`
}

// DeleteLocalRoomByRoute adds a room to the local visor/server
func (c Handler) DeleteLocalRoomByRoute(w http.ResponseWriter, r *http.Request) {
	var requestModel DeleteLocalRoomByRouteRequestModel
	decodeErr := json.NewDecoder(r.Body).Decode(&requestModel)
	if decodeErr != nil {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, decodeErr.Error())
		return
	}

	route := util.PKRoute{}

	visorpk := cipher.PubKey{}
	err := visorpk.Set(requestModel.VisorPk)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, err.Error())
		return
	}
	route.Visor = visorpk
	if requestModel.ServerPk != "" {
		serverpk := cipher.PubKey{}
		err = serverpk.Set(requestModel.ServerPk)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, err.Error())
			return
		}
		route.Server = serverpk
		if requestModel.RoomPk != "" {
			roompk := cipher.PubKey{}
			err = roompk.Set(requestModel.RoomPk)
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				fmt.Fprint(w, err.Error())
				return
			}
			route.Room = roompk
		}
	}

	err = c.chatServices.Commands.DeleteLocalRoomHandler.Handle(commands.DeleteLocalRoomRequest{
		Route: route,
	})
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, err.Error())
		return
	}
	w.WriteHeader(http.StatusOK)
}

// DeleteLocalServerByRouteURLParam contains the parameter identifier to be parsed by the handler
const DeleteLocalServerByRouteURLParam = "deleteLocalServer"

// DeleteLocalServerByRouteRequestModel represents the request model expected for Delete request
type DeleteLocalServerByRouteRequestModel struct {
	VisorPk  string `json:"visorpk"`
	ServerPk string `json:"serverpk"`
}

// DeleteLocalServerByRoute adds a room to the local visor/server
func (c Handler) DeleteLocalServerByRoute(w http.ResponseWriter, r *http.Request) {
	var requestModel DeleteLocalServerByRouteRequestModel
	decodeErr := json.NewDecoder(r.Body).Decode(&requestModel)
	if decodeErr != nil {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, decodeErr.Error())
		return
	}

	route := util.PKRoute{}

	visorpk := cipher.PubKey{}
	err := visorpk.Set(requestModel.VisorPk)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, err.Error())
		return
	}
	route.Visor = visorpk
	if requestModel.ServerPk != "" {
		serverpk := cipher.PubKey{}
		err = serverpk.Set(requestModel.ServerPk)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, err.Error())
			return
		}
		route.Server = serverpk
	}

	err = c.chatServices.Commands.DeleteLocalServerHandler.Handle(commands.DeleteLocalServerRequest{
		Route: route,
	})
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, err.Error())
		return
	}
	w.WriteHeader(http.StatusOK)
}

// DeleteVisorByPKURLParam contains the parameter identifier to be parsed by the handler
const DeleteVisorByPKURLParam = "deleteVisor"

// DeleteVisorByPK Deletes the remote visor with the provided pk
func (c Handler) DeleteVisorByPK(w http.ResponseWriter, r *http.Request) {
	fmt.Println(formatRequest(r))
	vars := mux.Vars(r)
	fmt.Println(vars)
	fmt.Println(vars[DeleteVisorByPKURLParam])
	chatPK := cipher.PubKey{}
	err := chatPK.Set(vars[DeleteVisorByPKURLParam])
	if err != nil {
		fmt.Println("could not convert pubkey")
	}
	fmt.Println(chatPK.Hex())
	err = c.chatServices.Commands.DeleteRemoteVisorHandler.Handle(commands.DeleteRemoteVisorRequest{Pk: chatPK})
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, err.Error())
	}
}

// LeaveRemoteRoomByRouteURLParam contains the parameter identifier to be parsed by the handler
const LeaveRemoteRoomByRouteURLParam = "leaveRemoteRoom"

// LeaveRemoteRoomByRouteRequestModel represents the request model expected for Leave request
type LeaveRemoteRoomByRouteRequestModel struct {
	VisorPk  string `json:"visorpk"`
	ServerPk string `json:"serverpk"`
	RoomPk   string `json:"roompk"`
}

// LeaveRemoteRoomByRoute adds a room to the local visor/server
func (c Handler) LeaveRemoteRoomByRoute(w http.ResponseWriter, r *http.Request) {
	var requestModel LeaveRemoteRoomByRouteRequestModel
	decodeErr := json.NewDecoder(r.Body).Decode(&requestModel)
	if decodeErr != nil {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, decodeErr.Error())
		return
	}

	route := util.PKRoute{}

	visorpk := cipher.PubKey{}
	err := visorpk.Set(requestModel.VisorPk)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, err.Error())
		return
	}
	route.Visor = visorpk
	if requestModel.ServerPk != "" {
		serverpk := cipher.PubKey{}
		err = serverpk.Set(requestModel.ServerPk)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, err.Error())
			return
		}
		route.Server = serverpk
		if requestModel.RoomPk != "" {
			roompk := cipher.PubKey{}
			err = roompk.Set(requestModel.RoomPk)
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				fmt.Fprint(w, err.Error())
				return
			}
			route.Room = roompk
		}
	}

	err = c.chatServices.Commands.LeaveRemoteRoomHandler.Handle(commands.LeaveRemoteRoomRequest{
		Route: route,
	})
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, err.Error())
		return
	}
	w.WriteHeader(http.StatusOK)
}

// LeaveRemoteServerByRouteURLParam contains the parameter identifier to be parsed by the handler
const LeaveRemoteServerByRouteURLParam = "leaveRemoteRoom"

// LeaveRemoteServerByRouteRequestModel represents the request model expected for Leave request
type LeaveRemoteServerByRouteRequestModel struct {
	VisorPk  string `json:"visorpk"`
	ServerPk string `json:"serverpk"`
}

// LeaveRemoteServerByRoute adds a room to the local visor/server
func (c Handler) LeaveRemoteServerByRoute(w http.ResponseWriter, r *http.Request) {
	var requestModel LeaveRemoteServerByRouteRequestModel
	decodeErr := json.NewDecoder(r.Body).Decode(&requestModel)
	if decodeErr != nil {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, decodeErr.Error())
		return
	}

	route := util.PKRoute{}

	visorpk := cipher.PubKey{}
	err := visorpk.Set(requestModel.VisorPk)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, err.Error())
		return
	}
	route.Visor = visorpk
	if requestModel.ServerPk != "" {
		serverpk := cipher.PubKey{}
		err = serverpk.Set(requestModel.ServerPk)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, err.Error())
			return
		}
		route.Server = serverpk
	}

	err = c.chatServices.Commands.LeaveRemoteServerHandler.Handle(commands.LeaveRemoteServerRequest{
		Route: route,
	})
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, err.Error())
		return
	}
	w.WriteHeader(http.StatusOK)
}

//[]:rename to SendTextMessageURLParam

// SendTextMessagePKURLParam contains the parameter identifier to be parsed by the handler
const SendTextMessagePKURLParam = "sendTxtMsg"

// SendTextMessageRequestModel represents the request model expected for Add request
type SendTextMessageRequestModel struct {
	VisorPk  string `json:"visorpk"`
	ServerPk string `json:"serverpk"`
	RoomPk   string `json:"roompk"`
	Msg      string `json:"message"`
}

// SendTextMessage sends a message to the provided pk
func (c Handler) SendTextMessage(w http.ResponseWriter, r *http.Request) {
	fmt.Println(formatRequest(r))
	var msgToSend SendTextMessageRequestModel
	decodeErr := json.NewDecoder(r.Body).Decode(&msgToSend)
	if decodeErr != nil {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, decodeErr.Error())
		return
	}

	visorpk := cipher.PubKey{}
	err := visorpk.Set(msgToSend.VisorPk)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, err.Error())
		return
	}

	serverpk := cipher.PubKey{}
	err = serverpk.Set(msgToSend.ServerPk)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, err.Error())
		return
	}

	roompk := cipher.PubKey{}
	err = roompk.Set(msgToSend.RoomPk)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, err.Error())
		return
	}

	err = c.chatServices.Commands.SendTextMessageHandler.Handle(commands.SendTextMessageRequest{
		Route: util.NewRoomRoute(visorpk, serverpk, roompk),
		Msg:   []byte(msgToSend.Msg),
	})
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, err.Error())
		return
	}
	w.WriteHeader(http.StatusOK)
}

// GetAllMessagesFromRoomByRouteURLParam contains the parameter identifier to be parsed by the handler
const GetAllMessagesFromRoomByRouteURLParam = "getRoomMessages"

// GetAllMessagesFromRoomByRouteRequestModel represents the request model expected for Get request
type GetAllMessagesFromRoomByRouteRequestModel struct {
	VisorPk  string `json:"visorpk"`
	ServerPk string `json:"serverpk"`
	RoomPk   string `json:"roompk"`
}

// GetAllMessagesFromRoomByRoute returns the server of the provided route
func (c Handler) GetAllMessagesFromRoomByRoute(w http.ResponseWriter, r *http.Request) {
	var routeToGet GetAllMessagesFromRoomByRouteRequestModel
	decodeErr := json.NewDecoder(r.Body).Decode(&routeToGet)
	if decodeErr != nil {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, decodeErr.Error())
		return
	}

	route := util.PKRoute{}

	visorpk := cipher.PubKey{}
	err := visorpk.Set(routeToGet.VisorPk)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, err.Error())
		return
	}
	route.Visor = visorpk
	if routeToGet.ServerPk != "" {
		serverpk := cipher.PubKey{}
		err = serverpk.Set(routeToGet.ServerPk)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, err.Error())
			return
		}
		route.Server = serverpk
		if routeToGet.RoomPk != "" {
			roompk := cipher.PubKey{}
			err = roompk.Set(routeToGet.RoomPk)
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				fmt.Fprint(w, err.Error())
				return
			}
			route.Room = roompk
		}
	}
	messages, err := c.chatServices.Queries.GetAllMessagesFromRoomHandler.Handle(queries.GetAllMessagesFromRoomRequest{
		Route: route,
	})
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, err.Error())
		return
	}
	err = json.NewEncoder(w).Encode(messages)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, err.Error())
		return
	}
}

// GetAllVisors Returns all available visors
func (c Handler) GetAllVisors(w http.ResponseWriter, r *http.Request) {
	fmt.Println(formatRequest(r))
	visors, err := c.chatServices.Queries.GetAllVisorsHandler.Handle()
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, err.Error())
		return
	}

	err = json.NewEncoder(w).Encode(visors)
	if err != nil {
		return
	}
}

// GetRoomByRouteURLParam contains the parameter identifier to be parsed by the handler
const GetRoomByRouteURLParam = "getRoom"

// GetRoomByRoute returns the server of the provided route
func (c Handler) GetRoomByRoute(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	qVisor := query.Get("visor")
	qServer := query.Get("server")
	qRoom := query.Get("room")

	route := util.PKRoute{}

	visorpk := cipher.PubKey{}
	err := visorpk.Set(qVisor)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, err.Error())
		return
	}
	route.Visor = visorpk
	if qServer != "" {
		serverpk := cipher.PubKey{}
		err = serverpk.Set(qServer)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, err.Error())
			return
		}
		route.Server = serverpk
		if qRoom != "" {
			roompk := cipher.PubKey{}
			err = roompk.Set(qRoom)
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				fmt.Fprint(w, err.Error())
				return
			}
			route.Room = roompk
		}
	}
	room, err := c.chatServices.Queries.GetRoomByRouteHandler.Handle(queries.GetRoomByRouteRequest{
		Route: route,
	})
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, err.Error())
		return
	}
	err = json.NewEncoder(w).Encode(room)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, err.Error())
		return
	}
}

// GetServerByRouteURLParam contains the parameter identifier to be parsed by the handler
const GetServerByRouteURLParam = "getServer"

// GetServerByRouteRequestModel represents the request model expected for Get request
type GetServerByRouteRequestModel struct {
	VisorPk  string `json:"visorpk"`
	ServerPk string `json:"serverpk"`
}

// GetServerByRoute returns the server of the provided route
func (c Handler) GetServerByRoute(w http.ResponseWriter, r *http.Request) {
	var routeToGet GetServerByRouteRequestModel
	decodeErr := json.NewDecoder(r.Body).Decode(&routeToGet)
	if decodeErr != nil {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, decodeErr.Error())
		return
	}

	route := util.PKRoute{}

	visorpk := cipher.PubKey{}
	err := visorpk.Set(routeToGet.VisorPk)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, err.Error())
		return
	}
	route.Visor = visorpk
	if routeToGet.ServerPk != "" {
		serverpk := cipher.PubKey{}
		err = serverpk.Set(routeToGet.ServerPk)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, err.Error())
			return
		}
		route.Server = serverpk
	}

	server, err := c.chatServices.Queries.GetServerByRouteHandler.Handle(queries.GetServerByRouteRequest{
		Route: route,
	})
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, err.Error())
		return
	}
	err = json.NewEncoder(w).Encode(server)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, err.Error())
		return
	}
}

// GetVisorByPKURLParam contains the parameter identifier to be parsed by the handler
const GetVisorByPKURLParam = "getVisor"

// GetVisorByPK Returns the chat with the provided pk
func (c Handler) GetVisorByPK(w http.ResponseWriter, r *http.Request) {
	fmt.Println(formatRequest(r))
	vars := mux.Vars(r)
	pk := cipher.PubKey{}
	err := pk.Set(vars[GetVisorByPKURLParam])
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, err.Error())
		return
	}

	chat, err := c.chatServices.Queries.GetVisorByPKHandler.Handle(queries.GetVisorByPKRequest{Pk: pk})

	/*if err == nil && chat == nil {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, "Not Found")
		return
	}*/

	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, err.Error())
		return
	}
	err = json.NewEncoder(w).Encode(chat)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, err.Error())
		return
	}
}

// formatRequest generates ascii representation of a request
func formatRequest(r *http.Request) string {
	// Create return string
	var request []string // Add the request string
	request = append(request, "--------------------------------------\n")
	url := fmt.Sprintf("%v %v %v", r.Method, r.URL, r.Proto)
	request = append(request, url)                             // Add the host
	request = append(request, fmt.Sprintf("Host: %v", r.Host)) // Loop through headers
	for name, headers := range r.Header {
		name = strings.ToLower(name)
		for _, h := range headers {
			request = append(request, fmt.Sprintf("%v: %v", name, h))
		}
	}
	// If this is a POST, add post data
	if r.Method == "POST" {
		r.ParseForm() //nolint
		request = append(request, "\n")
		request = append(request, r.Form.Encode())
	} // Return the request as a string
	request = append(request, "--------------------------------------\n")
	return strings.Join(request, "\n")
}
