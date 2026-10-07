// Package logserver pkg/visor/logserver/loglevel.go c3-vis-core
package logserver

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/skycoin/skywire/pkg/httputil"
	"github.com/skycoin/skywire/pkg/logging"
)

// DefaultTempLogLevelTTL is how long POST /debug/loglevel holds a level when
// the request names no ttl.
const DefaultTempLogLevelTTL = 15 * time.Minute

// LogLevelStatus is the visor's log level as /debug/loglevel reports it.
// Base and Until are set only while a temporary level is in force: Base is
// the level the visor goes back to at Until.
type LogLevelStatus struct {
	Level string     `json:"level"`
	Base  string     `json:"base,omitempty"`
	Until *time.Time `json:"until,omitempty"`
}

// LogLevelController changes the visor's log level for a bounded time. A
// temporary level is never written to the config, so a restart ends it too.
type LogLevelController interface {
	LogLevelStatus() LogLevelStatus
	SetTempLogLevel(level logrus.Level, ttl time.Duration) (LogLevelStatus, error)
	ClearTempLogLevel() LogLevelStatus
}

// SetLogLevelController enables /debug/loglevel. Until it is called the
// routes answer 503.
func (api *API) SetLogLevelController(c LogLevelController) {
	api.logLevelController = c
}

// registerLogLevelRoutes mounts /debug/loglevel. It lets a survey-whitelisted
// key turn on debug logging for one visor while a user reproduces a problem,
// and then read it with /skywire.log?follow=1, instead of every visor logging
// at debug all the time:
//
//	GET    /debug/loglevel                      current level
//	POST   /debug/loglevel?level=debug&ttl=15m  set a level until ttl runs out
//	DELETE /debug/loglevel                      go back to the base level now
func (api *API) registerLogLevelRoutes(route func(string, http.HandlerFunc)) {
	route("GET /debug/loglevel", func(w http.ResponseWriter, req *http.Request) {
		if api.logLevelController == nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		writeLogLevelStatus(w, req, api.logLevelController.LogLevelStatus())
	})
	route("POST /debug/loglevel", func(w http.ResponseWriter, req *http.Request) {
		if api.logLevelController == nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		q := req.URL.Query()
		level, err := logging.LevelFromString(q.Get("level"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		ttl := DefaultTempLogLevelTTL
		if s := q.Get("ttl"); s != "" {
			if ttl, err = time.ParseDuration(s); err != nil {
				http.Error(w, fmt.Sprintf("invalid ttl %q: %v", s, err), http.StatusBadRequest)
				return
			}
		}
		st, err := api.logLevelController.SetTempLogLevel(level, ttl)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeLogLevelStatus(w, req, st)
	})
	route("DELETE /debug/loglevel", func(w http.ResponseWriter, req *http.Request) {
		if api.logLevelController == nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		writeLogLevelStatus(w, req, api.logLevelController.ClearTempLogLevel())
	})
}

func writeLogLevelStatus(w http.ResponseWriter, req *http.Request, st LogLevelStatus) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(st); err != nil {
		httputil.GetLogger(req).WithError(err).Error("failed to write log level status")
	}
}
