//go:build tinygo || js

// Package appserver pkg/app/appserver/proc_ingress_listen_tinygo.go
package appserver

import "net"

// listenIngress returns no listener on the TinyGo js/wasm target: a browser
// cannot net.Listen("tcp"), and in-process apps (RunModeInternal — the only
// mode in a browser visor) reach the visor over net.Pipe via
// appcommon.RegisterInProcessConn, not the TCP ingress. The proc manager's
// accept loop, Addr, and Close all guard a nil listener.
func listenIngress(_ string) (net.Listener, error) {
	return nil, nil
}
