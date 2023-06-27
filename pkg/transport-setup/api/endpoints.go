package api

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/transport/network"
	"github.com/skycoin/skywire/pkg/transport/setup"
)

// TransportRequest specifies a transport between two nodes and its type
type TransportRequest struct {
	From cipher.PubKey `json:"from" validate:"required"`
	To   cipher.PubKey `json:"to" validate:"required"`
	Type string        `json:"type" validate:"required"`
}

// UUIDRequest combines target visor and UUID of a transport there
type UUIDRequest struct {
	From cipher.PubKey `json:"from" validate:"required"`
	ID   uuid.UUID     `json:"id" validate:"required"`
}

func (api *API) getTransportRequest(r *http.Request) (TransportRequest, error) {
	var req TransportRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		return TransportRequest{}, err
	}
	err := api.validator.Struct(&req)
	return req, err
}

func (api *API) getUUIDRequest(r *http.Request) (UUIDRequest, error) {
	var req UUIDRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		return UUIDRequest{}, err
	}
	err := api.validator.Struct(&req)
	return req, err
}

func (api *API) addTransport(w http.ResponseWriter, r *http.Request) {
	req, err := api.getTransportRequest(r)
	if err != nil {
		api.badRequest(w, r, err)
		return
	}
	if req.From == req.To {
		api.badRequest(w, r, fmt.Errorf("source and destination keys are the same"))
	}
	result := &setup.TransportResponse{}
	rpcReq := setup.TransportRequest{RemotePK: req.To, Type: network.Type(req.Type)}
	api.visorResponse(w, r, req.From, "TransportGateway.AddTransport", rpcReq, result)
}

func (api *API) removeTransport(w http.ResponseWriter, r *http.Request) {
	req, err := api.getUUIDRequest(r)
	if err != nil {
		api.badRequest(w, r, err)
		return
	}
	result := &setup.BoolResponse{}
	rpcReq := setup.UUIDRequest{ID: req.ID}
	api.visorResponse(w, r, req.From, "TransportGateway.RemoveTransport", rpcReq, result)
}

func (api *API) getTransports(w http.ResponseWriter, r *http.Request) {
	pkParam := chi.URLParam(r, "pk")
	var pk cipher.PubKey
	if err := pk.UnmarshalText([]byte(pkParam)); err != nil {
		api.badRequest(w, r, err)
	}

	result := &[]setup.TransportResponse{}
	rpcReq := struct{}{}
	api.visorResponse(w, r, pk, "TransportGateway.GetTransports", rpcReq, result)
}
