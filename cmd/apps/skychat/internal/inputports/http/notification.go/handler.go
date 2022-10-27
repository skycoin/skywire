// Package notification is the http handler for inputports
package notification

import (
	"fmt"
	"net/http"

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

// SubscribeNotifications sends all received msgs from channel to http
func (c Handler) SubscribeNotifications(w http.ResponseWriter, r *http.Request) {

	f, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported!", http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Transfer-Encoding", "chunked")

	for {
		select {
		case <-r.Context().Done():
			fmt.Println("SSE connection were closed.")
			return

		default:
			msg, ok := <-c.ns.GetChannel()
			if !ok {
				return
			}
			fmt.Fprintf(w, "data: %s\n\n", msg)
			f.Flush()
		}
	}
}
