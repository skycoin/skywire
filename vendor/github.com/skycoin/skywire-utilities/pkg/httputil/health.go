// Package httputil pkg/httputil/health.go
package httputil

import (
	"context"
	"net/http"
	"time"

	"github.com/skycoin/skywire-utilities/pkg/buildinfo"
)

var path = "/health"

// HealthCheckResponse is struct of /health endpoint
type HealthCheckResponse struct {
	BuildInfo *buildinfo.Info `json:"build_info,omitempty"`
	StartedAt time.Time       `json:"started_at"`
	DmsgAddr  string          `json:"dmsg_address,omitempty"`
}

// GetServiceHealth gets the response from the given service url
func GetServiceHealth(ctx context.Context, url string) (health *HealthCheckResponse, err error) {
	resp, err := http.Get(url + path)
	if err != nil {
		return nil, err
	}
	if resp != nil {
		defer func() {
			if cErr := resp.Body.Close(); cErr != nil && err == nil {
				err = cErr
			}
		}()
	}
	if resp.StatusCode != http.StatusOK {
		var hErr HTTPError
		if err = json.NewDecoder(resp.Body).Decode(&hErr); err != nil {
			return nil, err
		}
		return nil, &hErr
	}
	err = json.NewDecoder(resp.Body).Decode(&health)

	return health, nil
}
