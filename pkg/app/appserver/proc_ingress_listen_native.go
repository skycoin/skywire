//go:build !tinygo

// Package appserver pkg/app/appserver/proc_ingress_listen_native.go
package appserver

import "net"

// listenIngress opens the proc manager's app-ingress TCP listener. External
// (os/exec) apps dial it to reach the visor; in-process apps use net.Pipe
// instead (appcommon.RegisterInProcessConn), so the TinyGo build returns no
// listener — a browser can't net.Listen anyway. See proc_ingress_listen_tinygo.go.
func listenIngress(addr string) (net.Listener, error) {
	return net.Listen("tcp", addr)
}
