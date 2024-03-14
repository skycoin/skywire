package notification

import (
	"fmt"
	"net/http"
)

// SubscribeNotificationsSSE sends all notifications from the app to http as sse
func (c Handler) SubscribeNotificationsSSE(w http.ResponseWriter, r *http.Request) {

	fmt.Println("Get handshake from client")

	// prepare the flusher
	f, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported!", http.StatusInternalServerError)
		return
	}

	// prepare the header
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET")
	w.Header().Set("Access-Control-Expose-Headers", "Content-Type")
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Transfer-Encoding", "chunked")

	// instantiate the channel
	c.ns.InitChannel()

	// close the channel after exit the function
	defer func() {
		c.ns.DeferChannel()
	}()

	for {
		select {
		case msg, ok := <-c.ns.GetChannel():
			if !ok {
				fmt.Println("GetChannel not ok")
				return
			}
			fmt.Fprintf(w, "data: %s\n\n", msg)
			f.Flush()
		case <-r.Context().Done():
			fmt.Println("SSE: connection was closed.")
			return
		}
	}
}
