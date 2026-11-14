// Package cmdutil pkg/cmdutil/catch.go c1-com-util
package cmdutil

import (
	"fmt"
	"os"
	"strings"

	"github.com/skycoin/skywire/pkg/logging"
)

// Catch panics on any non-nil error.
func Catch(v ...any) {
	CatchWithMsg("", v...)
}

// CatchWithMsg panics on any non-nil error with the provided message (if any).
func CatchWithMsg(msg string, v ...any) {
	for _, val := range v {
		if err, ok := val.(error); ok && err != nil {
			if msg == "" {
				panic(err)
			}
			msg = strings.TrimSuffix(strings.TrimSpace(msg), ":")
			panic(fmt.Errorf("%s: %v", msg, err))
		}
	}
}

// CatchWithLog calls Fatal() on any non-nil error.
func CatchWithLog(log logging.FieldLogger, msg string, v ...any) {
	for _, val := range v {
		if err, ok := val.(error); ok && err != nil {
			log.WithError(err).Fatal(msg)
			os.Exit(1)
		}
	}
}
