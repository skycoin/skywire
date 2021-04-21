package httputil

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/middleware"
	jsoniter "github.com/json-iterator/go"
	"github.com/sirupsen/logrus"
	"github.com/skycoin/skycoin/src/util/logging"
)

var json = jsoniter.ConfigFastest

var log = logging.MustGetLogger("httputil")

// WriteJSON writes a json object on a http.ResponseWriter with the given code,
// panics on marshaling error
func WriteJSON(w http.ResponseWriter, r *http.Request, code int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	enc := json.NewEncoder(w)
	pretty, err := BoolFromQuery(r, "pretty", false)
	if err != nil {
		log.WithError(err).Warn("Failed to get bool from query")
	}
	if pretty {
		enc.SetIndent("", "  ")
	}
	if err, ok := v.(error); ok {
		v = map[string]interface{}{"error": err.Error()}
	}
	if err := json.NewEncoder(w).Encode(v); err != nil {
		panic(err)
	}
}

// ReadJSON reads the request body to a json object.
func ReadJSON(r *http.Request, v interface{}) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

// BoolFromQuery obtains a boolean from a query entry.
func BoolFromQuery(r *http.Request, key string, defaultVal bool) (bool, error) {
	switch q := r.URL.Query().Get(key); q {
	case "true", "on", "1":
		return true, nil
	case "false", "off", "0":
		return false, nil
	case "":
		return defaultVal, nil
	default:
		return false, fmt.Errorf("invalid '%s' query value of '%s'", key, q)
	}
}

// SplitRPCAddr returns host and port and whatever error results from parsing the rpc address interface
func SplitRPCAddr(rpcAddr string) (host string, port uint16, err error) {
	addrToken := strings.Split(rpcAddr, ":")
	uint64port, err := strconv.ParseUint(addrToken[1], 10, 16)
	if err != nil {
		return
	}

	return addrToken[0], uint16(uint64port), nil
}

type ctxKeyLogger int

// LoggerKey defines logger HTTP context key.
const LoggerKey ctxKeyLogger = -1

// GetLogger returns logger from HTTP context.
func GetLogger(r *http.Request) logrus.FieldLogger {
	if log, ok := r.Context().Value(LoggerKey).(logrus.FieldLogger); ok && log != nil {
		return log
	}

	return logging.NewMasterLogger()
}

// todo: investigate if it's used throughout the services (didn't work properly for UT)
// remove and use structured logging

// SetLoggerMiddleware sets logger to context of HTTP requests.
func SetLoggerMiddleware(log logrus.FieldLogger) func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		fn := func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()

			if reqID := middleware.GetReqID(ctx); reqID != "" && log != nil {
				ctx = context.WithValue(ctx, LoggerKey, log.WithField("RequestID", reqID))
			}
			next.ServeHTTP(w, r.WithContext(ctx))
		}
		return http.HandlerFunc(fn)
	}
}
