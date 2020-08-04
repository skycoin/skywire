package httputil

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"path"

	"github.com/sirupsen/logrus"
	"github.com/skycoin/skycoin/src/util/logging"
)

// HealthGrabberFunc grabs a component's health.
type HealthGrabberFunc func(ctx context.Context) (statusCode int, bodyMsg string)

// HealthGrabberEntry adds a 'Name' field to the HealthGrabber.
type HealthGrabberEntry struct {
	Name string
	Grab HealthGrabberFunc
}

// MakeHealthHandler returns a HTTP handler that displays component status(es).
// The endpoint returns a content type of text/plain with each component's
// health on a new line. The format of a line is as follows:
//	<componentName>: <HTTPStatusCode> <statusMessage>
// One can request the endpoint to return the health of a single component only
// via the following path:
//	/<expectedBase>/<componentName>
func MakeHealthHandler(log logrus.FieldLogger, expectedBase string, entries []HealthGrabberEntry) http.HandlerFunc {
	if log == nil {
		log = logging.MustGetLogger("health")
	}

	baseMap := make(map[string]HealthGrabberFunc, len(entries))
	for _, e := range entries {
		switch e.Name {
		case "", expectedBase:
			panic(errors.New("entry name cannot be empty or the same as expected base"))
		}

		baseMap[e.Name] = e.Grab
	}

	return func(w http.ResponseWriter, req *http.Request) {
		switch base := path.Base(req.URL.EscapedPath()); base {
		case "", "/", expectedBase:
			msg := ""
			for _, e := range entries {
				code, m := e.Grab(req.Context())
				msg += formatMsg(e.Name, code, m)

				if code < 200 || code > 299 {
					if err := writeHTTPText(w, code, msg); err != nil {
						log.WithError(err).Warn("Failed to write response body.")
					}
					return
				}
			}

			if err := writeHTTPText(w, http.StatusOK, msg); err != nil {
				log.WithError(err).Warn("Failed to write response body.")
			}

		default:
			grab, ok := baseMap[base]
			if !ok {
				msg := fmt.Sprintf("unexpected path base: %s", base)
				if err := writeHTTPText(w, http.StatusBadRequest, msg); err != nil {
					log.WithError(err).Warn("Failed to write response body.")
				}
				return
			}

			code, msg := grab(req.Context())
			if err := writeHTTPText(w, code, formatMsg(base, code, msg)); err != nil {
				log.WithError(err).Warn("Failed to write response body.")
			}
		}
	}
}

// CheckHealth calls the health endpoint at given URL, and writes the response
// into the provided writer (if nothing went wrong).
func CheckHealth(urlStr string, w io.Writer) error {
	resp, err := http.Get(urlStr) //nolint:gosec
	if err != nil {
		return err
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			fmt.Println("failed to close response body:", err)
		}
	}()

	b, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	if resp.StatusCode == http.StatusBadRequest {
		return fmt.Errorf("bad request: %s", string(b))
	}

	_, err = w.Write(b)
	return err
}

func formatMsg(name string, code int, msg string) string {
	return fmt.Sprintf("%s: %d %s\n", name, code, msg)
}

func writeHTTPText(w http.ResponseWriter, code int, msg string) error {
	w.WriteHeader(code)
	w.Header().Set("Content-Type", "text/plain")
	if _, err := w.Write([]byte(msg)); err != nil {
		return err
	}
	return nil
}
