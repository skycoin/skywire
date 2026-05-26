//go:build !js

// Package disc pkg/disc/http_message.go
//
// HTTP-response message types used by the discovery service's HTTP
// API. Build-tag-gated alongside client.go to keep net/http out of
// the WASM build graph (see interface.go for rationale).
package disc

import (
	"fmt"
	"net/http"
)

// Exposed Http messages
var (
	MsgEntrySet     = HTTPMessage{Code: http.StatusOK, Message: "wrote a new entry"}
	MsgEntryUpdated = HTTPMessage{Code: http.StatusOK, Message: "wrote new entry iteration"}
	MsgEntryDeleted = HTTPMessage{Code: http.StatusOK, Message: "deleted entry"}
)

// HTTPMessage represents a message to be returned as an http response
type HTTPMessage struct {
	Message string `json:"message"`
	Code    int    `json:"code"`
}

func (h HTTPMessage) String() string {
	return fmt.Sprintf("status code: %d. message: %s", h.Code, h.Message)
}
