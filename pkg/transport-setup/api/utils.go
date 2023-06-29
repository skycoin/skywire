package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/rpc"

	"github.com/sirupsen/logrus"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg"
	"github.com/skycoin/skywire/pkg/httputil"
	"github.com/skycoin/skywire/pkg/skyenv"
)

// visor RPC helpers

func (api *API) visorResponse(w http.ResponseWriter, r *http.Request, pk cipher.PubKey, method string, req, result interface{}) {
	if err := api.callVisorRPC(r.Context(), pk, method, req, result); err != nil {
		api.internalError(w, r, err)
		return
	}
	api.writeJSON(w, r, result)
}

func (api *API) callVisorRPC(ctx context.Context, pk cipher.PubKey, method string, req, result interface{}) error {
	port := skyenv.DmsgTransportSetupPort
	conn, err := api.dmsgC.Dial(ctx, dmsg.Addr{PK: pk, Port: port})
	if err != nil {
		return fmt.Errorf("error dialing remote visor: %w", err)
	}
	client := rpc.NewClient(conn)

	err = client.Call(method, req, result)
	if errors.Is(err, io.EOF) {
		return fmt.Errorf("remote connection closed (visor may not trust current pkey)")
	}
	if err != nil {
		return err
	}
	return nil
}

func (api *API) log(r *http.Request) logrus.FieldLogger {
	return httputil.GetLogger(r)
}

// Error handling helpers

// Error is the object returned to the client when there's an error.
type Error struct {
	Error string `json:"error"`
}

func (api *API) writeJSON(w http.ResponseWriter, r *http.Request, data interface{}) {
	if err := json.NewEncoder(w).Encode(data); err != nil {
		api.log(r).WithError(err).Error("Failed to encode response")
		api.internalError(w, r, err)
	}
}

func (api *API) internalError(w http.ResponseWriter, r *http.Request, err error) {
	api.writeError(w, r, http.StatusInternalServerError, err)
}

func (api *API) badRequest(w http.ResponseWriter, r *http.Request, err error) {
	api.writeError(w, r, http.StatusBadRequest, err)
}

func (api *API) writeError(w http.ResponseWriter, r *http.Request, status int, err error) {
	api.log(r).WithError(err).WithField("status", http.StatusText(status)).Error()
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(&Error{Error: err.Error()}); err != nil {
		api.log(r).WithError(err).Error("Failed to encode error")
	}
}
