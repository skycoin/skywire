// Package appnet pkg/app/appnet/http_transport.go
package appnet

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/skycoin/skywire-utilities/pkg/logging"
)

var passthruRequestHeaderKeys = []string{
	"Accept",
	"Accept-Encoding",
	"Accept-Language",
	"Cache-Control",
	"Cookie",
	"Referer",
	"User-Agent",
}

var passthruResponseHeaderKeys = []string{
	"Content-Encoding",
	"Content-Language",
	"Content-Type",
	"Cache-Control",
	"Date",
	"Etag",
	"Expires",
	"Last-Modified",
	"Location",
	"Server",
	"Vary",
}

// ServeProxy serves a HTTP forward proxy that accepts all requests and forwards them directly to the remote server over the specified net.Conn.
// The remoteConn is not closed here explicitly, so it should be handled outside.
func ServeProxy(log *logging.Logger, remoteConn net.Conn, localPort int) {

	handler := http.DefaultServeMux
	handler.HandleFunc("/", handleFunc(remoteConn, log))

	srv := &http.Server{
		Addr:           fmt.Sprintf(":%v", localPort),
		Handler:        handler,
		ReadTimeout:    10 * time.Second,
		WriteTimeout:   10 * time.Second,
		MaxHeaderBytes: 1 << 20,
	}

	go func() {
		err := srv.ListenAndServe()
		if err != nil {
			log.WithError(err).Error("Error listening and serving app proxy.")
		}
	}()
	log.Debugf("Serving on localhost:%v", localPort)
}

func handleFunc(remoteConn net.Conn, log *logging.Logger) func(w http.ResponseWriter, req *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		// Construct filtered header to send to remote server
		hh := http.Header{}
		for _, hk := range passthruRequestHeaderKeys {
			if hv, ok := r.Header[hk]; ok {
				hh[hk] = hv
			}
		}

		// Construct request to send to remote server
		rr := http.Request{
			Method:        r.Method,
			URL:           r.URL,
			Header:        hh,
			Body:          r.Body,
			ContentLength: r.ContentLength,
			Close:         r.Close,
		}
		client := http.Client{Transport: MakeHTTPTransport(remoteConn, log)}
		// Forward request to remote server
		resp, err := client.Transport.RoundTrip(&rr)
		if err != nil {
			http.Error(w, "Could not reach remote server", 500)
			return
		}

		defer func() {
			if err := resp.Body.Close(); err != nil {
				log.WithError(err).Errorln("Failed to close proxy response body")
			}
		}()

		// Transfer filtered header from remote server -> client
		respH := w.Header()
		for _, hk := range passthruResponseHeaderKeys {
			if hv, ok := resp.Header[hk]; ok {
				respH[hk] = hv
			}
		}
		w.WriteHeader(resp.StatusCode)

		// Transfer response from remote server -> client
		if resp.ContentLength > 0 {
			if _, err := io.CopyN(w, resp.Body, resp.ContentLength); err != nil {
				log.Warn(err)
			}
		} else if resp.Close {
			// Copy until EOF or some other error occurs
			for {
				if _, err := io.Copy(w, resp.Body); err != nil {
					break
				}
			}
		}
	}
}
