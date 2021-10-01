//go:build windows
// +build windows

package syslog

import (
	"errors"

	"github.com/sirupsen/logrus"
)

// SetupHook sets up syslog hook to the daemon on `addr`.
func SetupHook(addr, tag string) (logrus.Hook, error) {
	return nil, errors.New("syslog is not available for this OS")
}
