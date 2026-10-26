//go:build !linux

// Package hostbridge pkg/hostbridge/tun_other.go c3-vis-wasm
package hostbridge

import "errors"

var errNoTUN = errors.New("hostbridge: a host TUN is supported on Linux only")

type tunDevice struct{ name string }

func openTUN() (*tunDevice, error)             { return nil, errNoTUN }
func (t *tunDevice) Name() string              { return t.name }
func (t *tunDevice) Read([]byte) (int, error)  { return 0, errNoTUN }
func (t *tunDevice) Write([]byte) (int, error) { return 0, errNoTUN }
func (t *tunDevice) Close() error              { return nil }
func (t *tunDevice) release()                  {}
func tunSetup(string, string, int) error       { return errNoTUN }
func route(string, string, string) error       { return errNoTUN }
func defaultGateway() (string, error)          { return "", errNoTUN }
