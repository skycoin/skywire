package chat

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/gorilla/mux"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	chatservices "github.com/skycoin/skywire/cmd/apps/skychat/internal/app/chat"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/app/chat/commands"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/app/chat/queries"
)

//Handler Chat http request handler
type Handler struct {
	chatServices chatservices.ChatServices
}

// NewHandler Constructor returns *Handler
func NewHandler(cs chatservices.ChatServices) *Handler {
	return &Handler{chatServices: cs}
}

//GetAll Returns all available chats
func (c Handler) GetAll(w http.ResponseWriter, r *http.Request) {
	fmt.Println(formatRequest(r))
	chats, err := c.chatServices.Queries.GetAllChatsHandler.Handle()
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprintf(w, err.Error())
		return
	}

	err = json.NewEncoder(w).Encode(chats)
	if err != nil {
		return
	}
}

// GetChatPKURLParam contains the parameter identifier to be parsed by the handler
const GetChatPKURLParam = "chatPK"

//GetByPK Returns the chat with the provided pk
func (c Handler) GetByPK(w http.ResponseWriter, r *http.Request) {
	fmt.Println(formatRequest(r))
	vars := mux.Vars(r)
	pk := cipher.PubKey{}
	err := pk.Set(vars[GetChatPKURLParam])
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, err.Error())
		return
	}

	chat, err := c.chatServices.Queries.GetChatByPKHandler.Handle(queries.GetChatByPKRequest{Pk: pk})

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

//AddChatRequestModel represents the request model expected for Add request
type AddChatRequestModel struct {
	Pk string `json:"pk"`
}

//Add adds the provided pk
func (c Handler) Add(w http.ResponseWriter, r *http.Request) {
	//fmt.Println(formatRequest(r))
	var chatToAdd AddChatRequestModel
	decodeErr := json.NewDecoder(r.Body).Decode(&chatToAdd)
	if decodeErr != nil {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, decodeErr.Error())
		return
	}
	fmt.Println("HTTPHandler - Add - PK: " + chatToAdd.Pk)

	pk := cipher.PubKey{}
	err := pk.Set(chatToAdd.Pk)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, err.Error())
		return
	}

	err = c.chatServices.Commands.AddChatHandler.Handle(commands.AddChatRequest{
		Pk: pk,
	})
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, err.Error())
		return
	}
	w.WriteHeader(http.StatusOK)
}

//DeleteChatPKURLParam contains the parameter identifier to be parsed by the handler
const DeleteChatPKURLParam = "delete"

//Delete Deletes the crag with the provided id
func (c Handler) Delete(w http.ResponseWriter, r *http.Request) {
	fmt.Println(formatRequest(r))
	vars := mux.Vars(r)
	fmt.Println(vars)
	fmt.Println(vars[DeleteChatPKURLParam])
	chatPK := cipher.PubKey{}
	err := chatPK.Set(vars[DeleteChatPKURLParam])
	if err != nil {
		fmt.Println("could not convert pubkey")
	}
	fmt.Println(chatPK.Hex())
	err = c.chatServices.Commands.DeleteChatHandler.Handle(commands.DeleteChatRequest{Pk: chatPK})
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, err.Error())
	}

}

//SendTextMessagePKURLParam contains the parameter identifier to be parsed by the handler
const SendTextMessagePKURLParam = "sendTxtMsg"

//SendTextMessageRequestModel represents the request model expected for Add request
type SendTextMessageRequestModel struct {
	Pk  string `json:"pk"`
	Msg string `json:"message"`
}

//SendTextMessage sends a message to the provided pk
func (c Handler) SendTextMessage(w http.ResponseWriter, r *http.Request) {
	fmt.Println(formatRequest(r))
	var msgToSend SendTextMessageRequestModel
	decodeErr := json.NewDecoder(r.Body).Decode(&msgToSend)
	if decodeErr != nil {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, decodeErr.Error())
		return
	}

	pk := cipher.PubKey{}
	err := pk.Set(msgToSend.Pk)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, err.Error())
		return
	}

	err = c.chatServices.Commands.SendTextHandler.Handle(commands.SendTextMessageRequest{
		Pk:  pk,
		Msg: []byte(msgToSend.Msg),
	})
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, err.Error())
		return
	}
	w.WriteHeader(http.StatusOK)
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
