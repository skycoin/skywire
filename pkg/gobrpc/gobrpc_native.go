//go:build !tinygo

// Package gobrpc is a minimal, build-tagged stand-in for net/rpc's
// default (gob) codec.
//
// On every NON-TinyGo build this file makes the package a thin set of
// aliases to the standard library net/rpc — so the behavior, wire
// format, and concurrency semantics are byte-for-byte the real thing
// and there is zero functional change for native callers.
//
// On the TinyGo js/wasm target (gobrpc_tinygo.go) the same API aliases the
// dependency-free gobimpl reimplementation, because net/rpc pulls net/http
// which does not compile on that target. gobimpl speaks the identical gob
// wire protocol, so a TinyGo gobrpc client/server interoperates with a
// native net/rpc peer unchanged (verified in gobimpl/gobimpl_test.go).
//
// Only the surface the skywire router actually uses is exposed:
// NewClient, (*Client).Go/Call/Close, NewServer, (*Server).Register/
// ServeConn, the Call value, and ErrShutdown.
package gobrpc

import "net/rpc"

// Client is net/rpc.Client.
type Client = rpc.Client

// Server is net/rpc.Server.
type Server = rpc.Server

// Call is net/rpc.Call.
type Call = rpc.Call

// NewClient mirrors net/rpc.NewClient.
var NewClient = rpc.NewClient

// NewServer mirrors net/rpc.NewServer.
var NewServer = rpc.NewServer

// ErrShutdown mirrors net/rpc.ErrShutdown.
var ErrShutdown = rpc.ErrShutdown
