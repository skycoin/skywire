// Package cmdutil pkg/cmdutil/catch.go
package cmdutil

import (
	"fmt"
	"os"
	"strings"

	"github.com/sirupsen/logrus"
)

// Catch panics on any non-nil error.
func Catch(v ...interface{}) {
	CatchWithMsg("", v...)
}

// CatchWithMsg panics on any non-nil error with the provided message (if any).
func CatchWithMsg(msg string, v ...interface{}) {
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
func CatchWithLog(log logrus.FieldLogger, msg string, v ...interface{}) {
	for _, val := range v {
		if err, ok := val.(error); ok && err != nil {
			log.WithError(err).Fatal(msg)
			os.Exit(1)
		}
	}
}
