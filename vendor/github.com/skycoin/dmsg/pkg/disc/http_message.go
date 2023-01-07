// Package disc pkg/disc/http_message.go
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
