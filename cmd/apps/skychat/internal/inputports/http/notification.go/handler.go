// Package notification is the http handler for inputports
package notification

import (
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/app/notification"
)

// Handler Chat http request handler
type Handler struct {
	ns notification.Service
}

// NewHandler Constructor
func NewHandler(ns notification.Service) *Handler {
	return &Handler{ns: ns}
}
