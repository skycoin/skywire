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

// AddLocalServerURLParam contains the parameter identifier to be parsed by the handler
const AddLocalServerURLParam = "addLocalServer"

// AddLocalServerRequestModel represents the request model expected for Add request
type AddLocalServerRequestModel struct {
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

	info := info.Info{}
	info.SetAlias(requestModel.Alias)
	info.SetDescription(requestModel.Desc)
	info.SetImg(requestModel.Img)

	err := c.chatServices.Commands.AddLocalServerHandler.Handle(commands.AddLocalServerRequest{
		Info: info,
	})
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, err.Error())
		return
	}
	w.WriteHeader(http.StatusOK)
}

// JoinRemoteRouteURLParam contains the parameter identifier to be parsed by the handler
const JoinRemoteRouteURLParam = "joinRemoteRoute"

// JoinRemoteRouteRequestModel represents the request model expected for Join request
type JoinRemoteRouteRequestModel struct {
	VisorPk  string `json:"visorpk"`
	ServerPk string `json:"serverpk"`
	RoomPk   string `json:"roompk"`
}

// JoinRemoteRoute adds the provided route
func (c Handler) JoinRemoteRoute(w http.ResponseWriter, r *http.Request) {
	//fmt.Println(formatRequest(r))
	var routeToJoin JoinRemoteRouteRequestModel
	decodeErr := json.NewDecoder(r.Body).Decode(&routeToJoin)
	if decodeErr != nil {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, decodeErr.Error())
		return
	}

	route := util.PKRoute{}

	visorpk := cipher.PubKey{}
	err := visorpk.Set(routeToJoin.VisorPk)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, err.Error())
		return
	}
	route.Visor = visorpk
	if routeToJoin.ServerPk != "" {
		serverpk := cipher.PubKey{}
		err = serverpk.Set(routeToJoin.ServerPk)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, err.Error())
			return
		}
		route.Server = serverpk
		if routeToJoin.RoomPk != "" {
			roompk := cipher.PubKey{}
			err = roompk.Set(routeToJoin.RoomPk)
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				fmt.Fprint(w, err.Error())
				return
			}
			route.Room = roompk
		}
	}

	err = c.chatServices.Commands.JoinRemoteRouteHandler.Handle(commands.JoinRemoteRouteRequest{
		Route: route,
	})
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, err.Error())
		return
	}
	w.WriteHeader(http.StatusOK)
}

// SendDeleteRoomMessageURLParam contains the parameter identifier to be parsed by the handler
const SendDeleteRoomMessageURLParam = "sendDeleteRoomMessage"

// SendDeleteRoomMessageRequestModel represents the request model expected for Delete request
type SendDeleteRoomMessageRequestModel struct {
	VisorPk  string `json:"visorpk"`
	ServerPk string `json:"serverpk"`
	RoomPk   string `json:"roompk"`
}

// SendDeleteRoomMessage adds a room to the local visor/server
func (c Handler) SendDeleteRoomMessage(w http.ResponseWriter, r *http.Request) {
	var requestModel SendDeleteRoomMessageRequestModel
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

	err = c.chatServices.Commands.SendDeleteRoomMessageHandler.Handle(commands.SendDeleteRoomMessageRequest{
		Route: route,
	})
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, err.Error())
		return
	}
	w.WriteHeader(http.StatusOK)
}

// SendAddRoomMessageURLParam contains the parameter identifier to be parsed by the handler
const SendAddRoomMessageURLParam = "sendAddRoomMessage"

// SendAddRoomMessageRequestModel represents the request model expected for Delete request
type SendAddRoomMessageRequestModel struct {
	VisorPk  string `json:"visorpk"`
	ServerPk string `json:"serverpk"`
	RoomPk   string `json:"roompk"`

	//Info
	Alias string `json:"alias"`
	Desc  string `json:"desc"`
	Img   string `json:"img"`

	//Type
	Type string `json:"type"`
}

// SendAddRoomMessage adds a room to the local visor/server
func (c Handler) SendAddRoomMessage(w http.ResponseWriter, r *http.Request) {
	var requestModel SendAddRoomMessageRequestModel
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

	info := info.NewInfo(route.Room, requestModel.Alias, requestModel.Desc, requestModel.Img)

	var roomType int

	if requestModel.Type != "" {
		roomType, err = strconv.Atoi(requestModel.Type)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, err.Error())
			return
		}
	} else {
		roomType = 1
	}

	err = c.chatServices.Commands.SendAddRoomMessageHandler.Handle(commands.SendAddRoomMessageRequest{
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

// SendMutePeerMessageURLParam contains the parameter identifier to be parsed by the handler
const SendMutePeerMessageURLParam = "sendMutePeerMessage"

// SendMutePeerMessageRequestModel represents the request model expected for Delete request
type SendMutePeerMessageRequestModel struct {
	VisorPk  string `json:"visorpk"`
	ServerPk string `json:"serverpk"`
	RoomPk   string `json:"roompk"`

	PeerPk string `json:"peerpk"`
}

// SendMutePeerMessage sends a mute message to the given route
func (c Handler) SendMutePeerMessage(w http.ResponseWriter, r *http.Request) {
	var requestModel SendMutePeerMessageRequestModel
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

	peerpk := cipher.PubKey{}
	err = peerpk.Set(requestModel.PeerPk)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, err.Error())
		return
	}

	err = c.chatServices.Commands.SendMutePeerMessageHandler.Handle(commands.SendMutePeerMessageRequest{
		Route: route,
		Pk:    peerpk,
	})
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, err.Error())
		return
	}
	w.WriteHeader(http.StatusOK)
}

// SendUnmutePeerMessageURLParam contains the parameter identifier to be parsed by the handler
const SendUnmutePeerMessageURLParam = "sendUnmutePeerMessage"

// SendUnmutePeerMessageRequestModel represents the request model expected for Delete request
type SendUnmutePeerMessageRequestModel struct {
	VisorPk  string `json:"visorpk"`
	ServerPk string `json:"serverpk"`
	RoomPk   string `json:"roompk"`

	PeerPk string `json:"peerpk"`
}

// SendUnmutePeerMessage sends an unmute message to the given route
func (c Handler) SendUnmutePeerMessage(w http.ResponseWriter, r *http.Request) {
	var requestModel SendUnmutePeerMessageRequestModel
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

	peerpk := cipher.PubKey{}
	err = peerpk.Set(requestModel.PeerPk)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, err.Error())
		return
	}

	err = c.chatServices.Commands.SendUnmutePeerMessageHandler.Handle(commands.SendUnmutePeerMessageRequest{
		Route: route,
		Pk:    peerpk,
	})
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, err.Error())
		return
	}
	w.WriteHeader(http.StatusOK)
}

// DeleteLocalRouteURLParam contains the parameter identifier to be parsed by the handler
const DeleteLocalRouteURLParam = "DeleteLocalRoute"

// DeleteLocalRouteRequestModel represents the request model expected for Delete request
type DeleteLocalRouteRequestModel struct {
	VisorPk  string `json:"visorpk"`
	ServerPk string `json:"serverpk"`
	RoomPk   string `json:"roompk"`
}

// DeleteLocalRoute adds a room to the local visor/server
func (c Handler) DeleteLocalRoute(w http.ResponseWriter, r *http.Request) {
	var requestModel DeleteLocalRouteRequestModel
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

	err = c.chatServices.Commands.DeleteLocalRouteHandler.Handle(commands.DeleteLocalRouteRequest{
		Route: route,
	})
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, err.Error())
		return
	}
	w.WriteHeader(http.StatusOK)
}

// LeaveRemoteRouteURLParam contains the parameter identifier to be parsed by the handler
const LeaveRemoteRouteURLParam = "leaveRemoteRoute"

// LeaveRemoteRouteRequestModel represents the request model expected for Delete request
type LeaveRemoteRouteRequestModel struct {
	VisorPk  string `json:"visorpk"`
	ServerPk string `json:"serverpk"`
	RoomPk   string `json:"roompk"`
}

// LeaveRemoteRoute adds a room to the local visor/server
func (c Handler) LeaveRemoteRoute(w http.ResponseWriter, r *http.Request) {
	var requestModel LeaveRemoteRouteRequestModel
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

	err = c.chatServices.Commands.LeaveRemoteRouteHandler.Handle(commands.LeaveRemoteRouteRequest{
		Route: route,
	})
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, err.Error())
		return
	}
	w.WriteHeader(http.StatusOK)
}

// SendTextMessageURLParam contains the parameter identifier to be parsed by the handler
const SendTextMessageURLParam = "sendTxtMsg"

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
