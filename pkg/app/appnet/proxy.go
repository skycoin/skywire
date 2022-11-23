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

// ServeProxy serves a HTTP forward proxy that accepts all requests and forwards them directly to the remote server over the specified net.Conn.
func ServeProxy(log *logging.Logger, remoteConn net.Conn, localPort int) {
	closeChan := make(chan struct{})
	handler := http.NewServeMux()
	handler.HandleFunc("/", handleFunc(remoteConn, log, closeChan))

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
	go func() {
		<-closeChan
		err := srv.Close()
		if err != nil {
			log.Error(err)
		}
		err = remoteConn.Close()
		if err != nil {
			log.Error(err)
		}
	}()
	log.Debugf("Serving on localhost:%v", localPort)
}

func handleFunc(remoteConn net.Conn, log *logging.Logger, closeChan chan struct{}) func(w http.ResponseWriter, req *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		client := http.Client{Transport: MakeHTTPTransport(remoteConn, log)}
		// Forward request to remote server
		resp, err := client.Transport.RoundTrip(r)
		if err != nil {
			http.Error(w, "Could not reach remote server", 500)
			close(closeChan)
			return
		}

		defer func() {
			if err := resp.Body.Close(); err != nil {
				log.WithError(err).Errorln("Failed to close proxy response body")
			}
		}()

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
