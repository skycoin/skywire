//+build windows

package syslog

import (
	"errors"

	"github.com/sirupsen/logrus"
)

func SetupHook(addr, tag string) (logrus.Hook, error) {
	return nil, errors.New("syslog is not available for this OS")
}
