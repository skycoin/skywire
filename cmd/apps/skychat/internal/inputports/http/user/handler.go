package user

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	userservices "github.com/skycoin/skywire/cmd/apps/skychat/internal/app/user"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/app/user/commands"
)

//Handler User http request handler
type Handler struct {
	userServices userservices.UserServices
}

//NewHandler Constructor
func NewHandler(app userservices.UserServices) *Handler {
	return &Handler{userServices: app}
}

// GetSettingsURLParam contains the parameter identifier to be parsed by the handler
const GetSettingsURLParam = "settings"

//GetSettings Returns the settings of the user
func (c Handler) GetSettings(w http.ResponseWriter, r *http.Request) {
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

// GetInfoURLParam contains the parameter identifier to be parsed by the handler
const GetInfoURLParam = "info"

//GetInfo Returns the info of the user
func (c Handler) GetInfo(w http.ResponseWriter, r *http.Request) {
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

// SetInfoURLParam contains the parameter identifier to be parsed by the handler
const SetInfoURLParam = "info"

//SetInfoRequestModel represents the  request model of Update
type SetInfoRequestModel struct {
	Alias string `json:"alias"`
	Desc  string `json:"desc"`
	Img   string `json:"img"`
}

//SetInfo Updates the user's info with the provided data
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

// SetSettingsURLParam contains the parameter identifier to be parsed by the handler
const SetSettingsURLParam = "settings"

//SetSettingsRequestModel represents the  request model of SetSettings
type SetSettingsRequestModel struct {
	Blacklist string `json:"blacklist"`
}

//SetSettings sets the user's settings with the provided data
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
