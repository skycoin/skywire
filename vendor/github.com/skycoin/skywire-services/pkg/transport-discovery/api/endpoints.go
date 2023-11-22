package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
	"github.com/skycoin/skywire-utilities/pkg/buildinfo"
	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire-utilities/pkg/httpauth"
	"github.com/skycoin/skywire-utilities/pkg/httputil"
	"github.com/skycoin/skywire/pkg/transport"

	"github.com/skycoin/skywire-services/pkg/transport-discovery/store"
)

func (api *API) registerTransport(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		api.writeError(w, r, err)
		return
	}

	var entries []*transport.SignedEntry
	if err := json.Unmarshal(body, &entries); err != nil {
		api.writeError(w, r, err)
		return
	}

	for _, entry := range entries {
		if err := api.store.RegisterTransport(r.Context(), entry); err != nil {
			api.writeError(w, r, err)
			return
		}
	}

	w.WriteHeader(http.StatusCreated)
	if err := json.NewEncoder(w).Encode(entries); err != nil {
		api.writeError(w, r, err)
	}
}

func (api *API) getTransportByID(w http.ResponseWriter, r *http.Request) {
	idParam := chi.URLParam(r, "id")

	id, err := uuid.Parse(idParam)
	if err != nil {
		api.writeError(w, r, ErrInvalidTransportID)
		return
	}

	entry, err := api.store.GetTransportByID(r.Context(), id)
	if err != nil {
		api.writeError(w, r, err)
		return
	}

	if err := json.NewEncoder(w).Encode(entry); err != nil {
		api.writeError(w, r, err)
	}
}

func (api *API) getTransportByEdge(w http.ResponseWriter, r *http.Request) {
	edgeParam := chi.URLParam(r, "edge")

	pk := cipher.PubKey{}
	if err := pk.UnmarshalText([]byte(edgeParam)); err != nil {
		api.log(r).WithError(err).Error("Error parsing PK")
		api.writeError(w, r, ErrInvalidPubKey)
		return
	}

	entries, err := api.store.GetTransportsByEdge(r.Context(), pk)
	if err != nil {
		if err != store.ErrTransportNotFound {
			api.log(r).WithError(err).Error("Error getting transport")
		}
		api.writeError(w, r, err)
		return
	}
	if err := json.NewEncoder(w).Encode(entries); err != nil {
		api.log(r).WithError(err).Error("Error encoding entries")
		api.writeError(w, r, err)
	}
}

func (api *API) getAllTransports(w http.ResponseWriter, r *http.Request) {

	entries, err := api.store.GetAllTransports(r.Context())
	if err != nil {
		if err != store.ErrTransportNotFound {
			api.log(r).WithError(err).Error("Error getting transports")
		}
		api.writeError(w, r, err)
		return
	}
	if err := json.NewEncoder(w).Encode(entries); err != nil {
		api.log(r).WithError(err).Error("Error encoding entries")
		api.writeError(w, r, err)
	}
}

func (api *API) deleteTransport(w http.ResponseWriter, r *http.Request) {
	pk, ok := r.Context().Value(httpauth.ContextAuthKey).(cipher.PubKey)
	if !ok {
		api.writeError(w, r, errors.New("invalid auth, no public key provided"))
		return
	}

	idParam := chi.URLParam(r, "id")

	id, err := uuid.Parse(idParam)
	if err != nil {
		api.writeError(w, r, ErrInvalidTransportID)
		return
	}

	entry, err := api.store.GetTransportByID(r.Context(), id)
	if err != nil {
		api.writeError(w, r, err)
		return
	}

	if entry.EdgeIndex(pk) < 0 {
		api.writeError(w, r, ErrInvalidTransportID)
		return
	}

	err = api.store.DeregisterTransport(r.Context(), id)
	if err != nil {
		api.writeError(w, r, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	if _, err = w.Write([]byte("transport deleted")); err != nil {
		api.writeError(w, r, err)
	}
}

func (api *API) deregisterTransport(w http.ResponseWriter, r *http.Request) {
	api.log(r).Info("Deregistration process started.")

	nmPkString := r.Header.Get("NM-PK")
	if ok := WhitelistPKs.Get(nmPkString); !ok {
		api.log(r).WithError(ErrUnauthorizedNetworkMonitor).WithField("Step", "Checking NMs PK").Error("Deregistration process interrupt.")
		w.WriteHeader(http.StatusForbidden)
		return
	}

	nmPk := cipher.PubKey{}
	if err := nmPk.UnmarshalText([]byte(nmPkString)); err != nil {
		api.log(r).WithError(ErrBadInput).WithField("Step", "Reading NMs PK").Error("Deregistration process interrupt.")
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	nmSign := cipher.Sig{}
	if err := nmSign.UnmarshalText([]byte(r.Header.Get("NM-Sign"))); err != nil {
		api.log(r).WithError(ErrBadInput).WithField("Step", "Checking sign").Error("Deregistration process interrupt.")
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	if err := cipher.VerifyPubKeySignedPayload(nmPk, nmSign, []byte(nmPk.Hex())); err != nil {
		api.log(r).WithError(ErrUnauthorizedNetworkMonitor).WithField("Step", "Verifying request").Error("Deregistration process interrupt.")
		w.WriteHeader(http.StatusForbidden)
		return
	}

	var tps []string
	body, err := io.ReadAll(r.Body)
	if err != nil {
		api.log(r).WithError(ErrBadInput).WithField("Step", "Reading transports ids").Error("Deregistration process interrupt.")
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if err := json.Unmarshal(body, &tps); err != nil {
		api.log(r).WithError(ErrBadInput).WithField("Step", "Unmarshal transports ids").Error("Deregistration process interrupt.")
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	for _, idParam := range tps {
		id, err := uuid.Parse(idParam)
		if err != nil {
			api.writeError(w, r, ErrInvalidTransportID)
			continue
		}
		err = api.store.DeregisterTransport(r.Context(), id)
		if err != nil {
			api.writeError(w, r, err)
			continue
		}
	}

	api.log(r).WithFields(logrus.Fields{"Number of Transports": len(tps), "Transports": tps}).Info("Deregistration process completed.")
	api.writeJSON(w, r, http.StatusOK, nil)
}

func (api *API) health(w http.ResponseWriter, r *http.Request) {
	info := buildinfo.Get()
	api.writeJSON(w, r, http.StatusOK, HealthCheckResponse{
		BuildInfo: info,
		StartedAt: api.startedAt,
		DmsgAddr:  api.dmsgAddr,
	})
}

func (api *API) writeJSON(w http.ResponseWriter, r *http.Request, code int, object interface{}) {
	jsonObject, err := json.Marshal(object)
	if err != nil {
		api.logger(r).WithError(err).Errorf("failed to encode json response")
		w.WriteHeader(http.StatusInternalServerError)

		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)

	_, err = w.Write(jsonObject)
	if err != nil {
		api.logger(r).WithError(err).Errorf("failed to write json response")
	}
}

func (api *API) logger(r *http.Request) logrus.FieldLogger {
	return httputil.GetLogger(r)
}
