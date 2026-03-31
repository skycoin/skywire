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

// clientMsg is the initial request from client to server.
type clientMsg struct {
	Port   int  `json:"port"`
	RawTCP bool `json:"raw_tcp,omitempty"`
}

// serverReply is the server's response to a client connection request.
type serverReply struct {
	Error *string `json:"error,omitempty"`
}

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
