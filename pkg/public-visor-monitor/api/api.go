// Package api pkg/public-visor-monitor/api.go
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/sirupsen/logrus"

	"github.com/skycoin/skywire-utilities/pkg/buildinfo"
	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire-utilities/pkg/httputil"
	"github.com/skycoin/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/pkg/servicedisc"
	"github.com/skycoin/skywire/pkg/transport/network"
	"github.com/skycoin/skywire/pkg/visor"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

// API register all the API endpoints.
// It implements a net/http.Handler.
type API struct {
	http.Handler
	Config
	ServicesURLs

	Visor *visor.Visor

	pubVisorKeys  []cipher.PubKey
	deadPubVisors []string
	logger        logging.Logger
	startedAt     time.Time
}

// Config is struct for keys and sign value of PVM
type Config struct {
	PK   cipher.PubKey
	SK   cipher.SecKey
	Sign cipher.Sig
}

// ServicesURLs is struct for organizing URL's of services
type ServicesURLs struct {
	SD string
	UT string
}

// HealthCheckResponse is struct of /health endpoint
type HealthCheckResponse struct {
	BuildInfo *buildinfo.Info `json:"build_info,omitempty"`
	StartedAt time.Time       `json:"started_at,omitempty"`
}

// Error is the object returned to the client when there's an error.
type Error struct {
	Error string `json:"error"`
}

// New returns a new *chi.Mux object, which can be started as a server
func New(logger *logging.Logger, srvURLs ServicesURLs, vmConfig Config) *API {

	api := &API{
		Config:       vmConfig,
		ServicesURLs: srvURLs,
		logger:       *logger,
		startedAt:    time.Now(),
	}
	r := chi.NewRouter()

	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(httputil.SetLoggerMiddleware(logger))
	r.Get("/health", api.health)
	api.Handler = r

	return api
}

func (api *API) health(w http.ResponseWriter, r *http.Request) {
	info := buildinfo.Get()
	api.writeJSON(w, r, http.StatusOK, HealthCheckResponse{
		BuildInfo: info,
		StartedAt: api.startedAt,
	})
}

func (api *API) writeJSON(w http.ResponseWriter, r *http.Request, code int, object interface{}) {
	jsonObject, err := json.Marshal(object)
	if err != nil {
		api.log(r).WithError(err).Errorf("failed to encode json response")
		w.WriteHeader(http.StatusInternalServerError)

		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)

	_, err = w.Write(jsonObject)
	if err != nil {
		api.log(r).WithError(err).Errorf("failed to write json response")
	}
}

func (api *API) log(r *http.Request) logrus.FieldLogger {
	return httputil.GetLogger(r)
}

// InitDeregistrationLoop is function which runs periodic background tasks of API.
func (api *API) InitDeregistrationLoop(ctx context.Context, conf *visorconfig.V1, sleepDeregistration time.Duration) {
	// Start a visor
	api.startVisor(ctx, conf)

	for {
		select {
		case <-ctx.Done():
			return
		default:
			api.deregister()
			time.Sleep(sleepDeregistration * time.Minute)
		}
	}
}

// deregister dead Public Visor entries in service discovery
func (api *API) deregister() {
	api.logger.Info("Public Visor Deregistration started.")

	// reload keys
	api.getPublicVisorKeys()

	// monitoring Public Visors
	onlinePubVisorCount := int64(0)
	api.deadPubVisors = []string{}

	if len(api.pubVisorKeys) == 0 {
		api.logger.Warn("No Public Visor keys found")
	} else {
		for _, key := range api.pubVisorKeys {
			if ok := api.testPublicVisor(key); ok {
				onlinePubVisorCount++
			}
		}
		api.logger.WithField("count", onlinePubVisorCount).Info("Public Visors online.")

		// deregister dead Public Visors
		if len(api.deadPubVisors) > 0 {
			api.deregisterPublicVisor(api.deadPubVisors)
		}
	}

	api.logger.WithField("Number of dead Public Visors", len(api.deadPubVisors)).WithField("PKs", api.deadPubVisors).Info("Public Visor Deregistration completed.")
}

func (api *API) testPublicVisor(key cipher.PubKey) bool {

	online := api.isOnline(key)

	if !online {
		api.deadPubVisors = append(api.deadPubVisors, key.Hex())
	}
	return online
}

func (api *API) isOnline(key cipher.PubKey) bool {
	transport := network.STCPR

	tp, err := api.Visor.AddTransport(key, string(transport), time.Second*10)
	if err != nil {
		api.logger.WithError(err).Warnf("Failed to establish %v transport", transport)
		return false
	}

	err = api.Visor.RemoveTransport(tp.ID)
	if err != nil {
		api.logger.Warnf("Error removing %v transport of %v: %v", transport, key, err)
	}

	return true
}

func (api *API) deregisterPublicVisor(keys []string) {
	err := api.deregisterRequest(keys, fmt.Sprintf(api.ServicesURLs.SD+"/api/services/deregister/visor"))
	if err != nil {
		api.logger.Warn(err)
		return
	}
	api.logger.Info("Deregister request send to SD")
}

// deregisterRequest is deregistration handler for all services
func (api *API) deregisterRequest(keys []string, rawReqURL string) error {
	reqURL, err := url.Parse(rawReqURL)
	if err != nil {
		return fmt.Errorf("Error on parsing deregistration URL : %v", err)
	}

	jsonData, err := json.Marshal(keys)
	if err != nil {
		return fmt.Errorf("Error on parsing deregistration keys : %v", err)
	}
	body := bytes.NewReader(jsonData)

	req := &http.Request{
		Method: "DELETE",
		URL:    reqURL,
		Header: map[string][]string{
			"NM-PK":   {api.Config.PK.Hex()},
			"NM-Sign": {api.Config.Sign.Hex()},
		},
		Body: io.NopCloser(body),
	}

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("Error on send deregistration request : %s", err)
	}
	defer res.Body.Close() //nolint

	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("Error deregistering Public Visor keys: status code %v", res.StatusCode)
	}

	return nil
}

type pubVisorList []servicedisc.Service

func getPublicVisors(sdURL string) (data pubVisorList, err error) {
	res, err := http.Get(sdURL + "/api/services?type=visor") //nolint
	if err != nil {
		return nil, err
	}

	body, err := io.ReadAll(res.Body)

	if err != nil {
		return nil, err
	}

	err = json.Unmarshal(body, &data)
	if err != nil {
		return nil, err
	}
	return data, nil
}

func (api *API) getPublicVisorKeys() {
	pvs, err := getPublicVisors(api.ServicesURLs.SD)
	if err != nil {
		api.logger.Warn("Error while fetching Public visors: %v", err)
		return
	}
	if len(pvs) == 0 {
		api.logger.Warn("No public visors found... Trying again")
	}
	api.pubVisorKeys = []cipher.PubKey{}
	for _, pv := range pvs {
		api.pubVisorKeys = append(api.pubVisorKeys, pv.Addr.PubKey())
	}

	api.logger.WithField("public visors", len(pvs)).Info("Public Visor keys updated.")
}

func (api *API) startVisor(ctx context.Context, conf *visorconfig.V1) {
	conf.SetLogger(logging.NewMasterLogger())
	v, ok := visor.NewVisor(ctx, conf)
	if !ok {
		api.logger.Fatal("Failed to start visor.")
	}
	api.Visor = v
}
