//go:build !windows
// +build !windows

// Package syslog contains SetupHook
package syslog

import (
	"fmt"
	"log/syslog"

	"github.com/sirupsen/logrus"
	logrussyslog "github.com/sirupsen/logrus/hooks/syslog"
)

// SetupHook sets up syslog hook to the daemon on `addr`.
func SetupHook(addr, tag string) (logrus.Hook, error) {
	hook, err := logrussyslog.NewSyslogHook("udp", addr, syslog.LOG_INFO, tag)
	if err != nil {
		return nil, fmt.Errorf("unable to connect to syslog daemon on %s: %w", addr, err)
	}

	return hook, nil
}
