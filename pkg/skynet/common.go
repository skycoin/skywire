// Package skynet internal/skynet/common.go
package skynet

import (
	"net"
	"time"
)

// DefaultReadTimeout is the read deadline for HTTP forwarding responses.
// Higher than the old hardcoded 5s to accommodate high-latency DMSG routes.
const DefaultReadTimeout = 15 * time.Second

// MaxRequestSize limits incoming request bodies to prevent memory exhaustion.
const MaxRequestSize = 10 * 1024 * 1024 // 10 MB

// MaxResponseSize limits response buffering for HTTP forwarding.
const MaxResponseSize = 10 * 1024 * 1024 // 10 MB

// ClientMsg is the initial JSON request from client to server. Exported so
// in-process consumers (e.g. pkg/skynetweb) can speak the same protocol
// without redefining the wire format.
type ClientMsg struct {
	Port   int  `json:"port"`
	RawTCP bool `json:"raw_tcp,omitempty"`
}

// ServerReply is the server's JSON response to ClientMsg.
type ServerReply struct {
	Error *string `json:"error,omitempty"`
}

// Exported aliases keep the existing internal call sites working.
// The lower-case types used to be private; changing them to aliases
// preserves every field access without touching server.go / client.go
// callers and lets pkg/skynetweb import the canonical shape.
type (
	clientMsg   = ClientMsg
	serverReply = ServerReply
)

func timeoutAfter(d time.Duration) time.Time {
	return time.Now().Add(d)
}

func isTimeout(err error) bool {
	if err == nil {
		return false
	}
	netErr, ok := err.(net.Error)
	return ok && netErr.Timeout()
}
