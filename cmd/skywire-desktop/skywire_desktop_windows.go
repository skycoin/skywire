//+build darwin

package main

import (
	"errors"
)

var errMethodNotImplemented = errors.New("method not implemented")

func isVisorRunning() (bool, error) {
	return false, errMethodNotImplemented
}

func startVisorDaemon() error {
	return errMethodNotImplemented
}

func stopVisorDaemon() error {
	return errMethodNotImplemented
}
