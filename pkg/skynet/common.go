// Package skynet internal/skynet/common.go
package skynet

// MaxRequestSize limits incoming request bodies to prevent memory exhaustion.
const MaxRequestSize = 10 * 1024 * 1024 // 10 MB

// ClientMsg is the initial JSON request from client to server. Exported so
// in-process consumers (e.g. pkg/skynetweb) can speak the same protocol
// without redefining the wire format.
type ClientMsg struct {
	Port int `json:"port"`
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
