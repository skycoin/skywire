// Package gobrpc is a net/rpc compatible client and server speaking the
// stdlib gob codec. It aliases gobimpl, which builds under both Go and TinyGo
// and interoperates with a standard net/rpc peer (see gobimpl/gobimpl_test.go).
package gobrpc

import "github.com/0magnet/gobrpc/gobimpl"

// Client is gobimpl.Client (net/rpc-compatible).
type Client = gobimpl.Client

// Server is gobimpl.Server (net/rpc-compatible).
type Server = gobimpl.Server

// Call is gobimpl.Call (net/rpc-compatible).
type Call = gobimpl.Call

// HandlerFunc is gobimpl.HandlerFunc, a reflection-free RPC method handler.
// TinyGo cannot run reflection dispatch, so servers that build there register
// with (*Server).HandleFunc or HandleCall instead of Register.
type HandlerFunc = gobimpl.HandlerFunc

// CallFunc and DecodeFunc are the gobimpl types for (*Server).HandleCall, which
// runs each call in its own goroutine.
type (
	CallFunc   = gobimpl.CallFunc
	DecodeFunc = gobimpl.DecodeFunc
)

// ServerError mirrors net/rpc.ServerError.
type ServerError = gobimpl.ServerError

// NewClient mirrors net/rpc.NewClient.
var NewClient = gobimpl.NewClient

// NewServer mirrors net/rpc.NewServer.
var NewServer = gobimpl.NewServer

// Dial mirrors net/rpc.Dial.
var Dial = gobimpl.Dial

// ErrShutdown mirrors net/rpc.ErrShutdown.
var ErrShutdown = gobimpl.ErrShutdown
