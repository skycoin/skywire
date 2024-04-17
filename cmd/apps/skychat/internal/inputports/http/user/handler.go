// Package user is the http handler for inputports
package user

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/gorilla/mux"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	userservices "github.com/skycoin/skywire/cmd/apps/skychat/internal/app/user"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/app/user/commands"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/info"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/peer"
)

// Handler User http request handler
type Handler struct {
	userServices userservices.UserServices
}

// NewHandler Constructor
func NewHandler(app userservices.UserServices) *Handler {
	return &Handler{userServices: app}
}

// AddPeerURLParam contains the parameter identifier to be parsed by the handler
const AddPeerURLParam = "addPeer"

// AddPeerRequestModel represents the  request model of AddPeer
type AddPeerRequestModel struct {
	//Info (from peer)
	PK    string `json:"pk"`
	Alias string `json:"alias"`
	Desc  string `json:"desc"`
	Img   string `json:"img"`
	//Alias
	Custom string `json:"custom"`
}

// AddPeer adds the given peer with the provided data
func (c Handler) AddPeer(w http.ResponseWriter, r *http.Request) {

	var reqPeerToSet AddPeerRequestModel
	decodeErr := json.NewDecoder(r.Body).Decode(&reqPeerToSet)
	if decodeErr != nil {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, decodeErr)
		return
	}

	var pk cipher.PubKey
	err := pk.Set(reqPeerToSet.PK)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, err.Error())
		return
	}

	info := info.NewInfo(pk, reqPeerToSet.Alias, reqPeerToSet.Desc, reqPeerToSet.Img)

	peerAddCommand := commands.AddPeerRequest{Info: info, Alias: reqPeerToSet.Custom}

	err = c.userServices.Commands.AddPeerHandler.Handle(peerAddCommand)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, err.Error())
	}

	w.WriteHeader(http.StatusOK)
}

// DeletePeerURLParam contains the parameter identifier to be parsed by the handler
const DeletePeerURLParam = "deletePeer"

// DeletePeer deletes the provided peer
func (c Handler) DeletePeer(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	pk := cipher.PubKey{}
	err := pk.Set(vars[DeletePeerURLParam])
	if err != nil {
		fmt.Println("could not convert pubkey")
	}

	peerDeleteCommand := commands.DeletePeerRequest{PK: pk}

	err = c.userServices.Commands.DeletePeerHandler.Handle(peerDeleteCommand)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, err.Error())
	}

	w.WriteHeader(http.StatusOK)
}

// SetInfoURLParam contains the parameter identifier to be parsed by the handler
const SetInfoURLParam = "setInfo"

// SetInfoRequestModel represents the  request model of Update
type SetInfoRequestModel struct {
	Alias string `json:"alias"`
	Desc  string `json:"desc"`
	Img   string `json:"img"`
}

// SetInfo Updates the user's info with the provided data
func (c Handler) SetInfo(w http.ResponseWriter, r *http.Request) {

	var reqInfoToUpdate SetInfoRequestModel
	decodeErr := json.NewDecoder(r.Body).Decode(&reqInfoToUpdate)
	if decodeErr != nil {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, decodeErr)
		return
	}

	infoUpdateCommand := commands.SetInfoRequest{
		Alias: reqInfoToUpdate.Alias,
		Desc:  reqInfoToUpdate.Desc,
		Img:   reqInfoToUpdate.Img,
	}

	err := c.userServices.Commands.SetInfoHandler.Handle(infoUpdateCommand)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, err.Error())
	}
	w.WriteHeader(http.StatusOK)
}

// SetPeerURLParam contains the parameter identifier to be parsed by the handler
const SetPeerURLParam = "setPeer"

// SetPeerRequestModel represents the  request model of SetPeer
type SetPeerRequestModel struct {
	//Info (from peer)
	PK    string `json:"pk"`
	Alias string `json:"alias"`
	Desc  string `json:"desc"`
	Img   string `json:"img"`
	//Alias
	Custom string `json:"custom"`
}

// SetPeer updates the peer with the provided data
func (c Handler) SetPeer(w http.ResponseWriter, r *http.Request) {

	var reqPeerToSet SetPeerRequestModel
	decodeErr := json.NewDecoder(r.Body).Decode(&reqPeerToSet)
	if decodeErr != nil {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, decodeErr)
		return
	}

	var pk cipher.PubKey
	err := pk.Set(reqPeerToSet.PK)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, err.Error())
		return
	}

	info := info.NewInfo(pk, reqPeerToSet.Alias, reqPeerToSet.Desc, reqPeerToSet.Img)

	peer := *peer.NewPeer(info, reqPeerToSet.Custom)

	peerSetCommand := commands.SetPeerRequest{Peer: peer}

	err = c.userServices.Commands.SetPeerHandler.Handle(peerSetCommand)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, err.Error())
	}

	w.WriteHeader(http.StatusOK)
}

// SetSettingsURLParam contains the parameter identifier to be parsed by the handler
const SetSettingsURLParam = "setSettings"

// SetSettingsRequestModel represents the  request model of SetSettings
type SetSettingsRequestModel struct {
	Blacklist string `json:"blacklist"`
}

// SetSettings sets the user's settings with the provided data
func (c Handler) SetSettings(w http.ResponseWriter, r *http.Request) {

	var reqSettingsToSet SetSettingsRequestModel
	decodeErr := json.NewDecoder(r.Body).Decode(&reqSettingsToSet)
	if decodeErr != nil {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, decodeErr)
		return
	}

	var keys cipher.PubKeys
	err := keys.Set(reqSettingsToSet.Blacklist)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, err.Error())
	} else {
		setSettingsCommand := commands.SetSettingsRequest{
			Blacklist: keys,
		}

		err = c.userServices.Commands.SetSettingsHandler.Handle(setSettingsCommand)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, err.Error())
		}
	}
	w.WriteHeader(http.StatusOK)
}

// GetInfoURLParam contains the parameter identifier to be parsed by the handler
const GetInfoURLParam = "getInfo"

// GetInfo Returns the info of the user
func (c Handler) GetInfo(w http.ResponseWriter, _ *http.Request) {
	info, err := c.userServices.Queries.GetUserInfoHandler.Handle()
	if err == nil && info == nil {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, "Not Found")
		return
	}

	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, err.Error())
		return
	}
	err = json.NewEncoder(w).Encode(info)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, err.Error())
		return
	}
}

// GetPeerbookURLParam contains the parameter identifier to be parsed by the handler
const GetPeerbookURLParam = "getPeerbook"

// GetPeerbook returns the peerbook of the user
func (c Handler) GetPeerbook(w http.ResponseWriter, _ *http.Request) {
	info, err := c.userServices.Queries.GetUserPeerBookHandler.Handle()
	if err == nil && info == nil {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, "Not Found")
		return
	}

	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, err.Error())
		return
	}
	err = json.NewEncoder(w).Encode(info)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, err.Error())
		return
	}
}

// GetSettingsURLParam contains the parameter identifier to be parsed by the handler
const GetSettingsURLParam = "getSettings"

// GetSettings Returns the settings of the user
func (c Handler) GetSettings(w http.ResponseWriter, _ *http.Request) {
	settings, err := c.userServices.Queries.GetUserSettingsHandler.Handle()
	if err == nil && settings == nil {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, "Not Found")
		return
	}

	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, err.Error())
		return
	}
	err = json.NewEncoder(w).Encode(settings)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, err.Error())
		return
	}
}
