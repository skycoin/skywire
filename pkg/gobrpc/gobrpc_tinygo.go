//go:build tinygo

// On the TinyGo js/wasm target net/rpc is unavailable (it pulls net/http,
// which does not compile there), so pkg/gobrpc aliases to the dependency-free
// gobimpl reimplementation, which speaks the identical gob wire protocol. See
// gobrpc_native.go for the native (net/rpc) side.
package gobrpc

import "github.com/skycoin/skywire/pkg/gobrpc/gobimpl"

// Client is gobimpl.Client (net/rpc-compatible).
type Client = gobimpl.Client

// Server is gobimpl.Server (net/rpc-compatible).
type Server = gobimpl.Server

// Call is gobimpl.Call (net/rpc-compatible).
type Call = gobimpl.Call

// NewClient mirrors net/rpc.NewClient.
var NewClient = gobimpl.NewClient

// NewServer mirrors net/rpc.NewServer.
var NewServer = gobimpl.NewServer

// Dial mirrors net/rpc.Dial.
var Dial = gobimpl.Dial

// ErrShutdown mirrors net/rpc.ErrShutdown.
var ErrShutdown = gobimpl.ErrShutdown
