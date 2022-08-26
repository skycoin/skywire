// Package rpcutil rpcutil.go
package rpcutil

import (
	"time"

	"github.com/sirupsen/logrus"
)

// LogCall is used to log an RPC call from the rpc.Server
func LogCall(log logrus.FieldLogger, method string, in interface{}) func(out interface{}, err *error) {

	// Just in case log is not set.
	if log == nil {
		log = logrus.New()
	}

	start := time.Now()
	log = log.
		WithField("_method", method).
		WithField("_received", start.Format(time.Kitchen))
	if in != nil {
		log = log.WithField("input", in)
	}

	return func(out interface{}, err *error) {
		log := log.WithField("_elapsed", time.Since(start).String())
		if out != nil {
			log = log.WithField("output", out)
		}
		if err != nil && *err != nil {
			log = log.WithError(*err)
		}
		log.Trace("Request processed.")
	}
}
