// Package httputil pkg/httputil/health.go
package httputil

import (
	"context"
	"net/http"
	"time"

	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/buildinfo"
)

var path = "/health"

// HealthCheckResponse is struct of /health endpoint
type HealthCheckResponse struct {
	ServiceName       string          `json:"service_name,omitempty"`
	BuildInfo         *buildinfo.Info `json:"build_info,omitempty"`
	StartedAt         time.Time       `json:"started_at"`
	DmsgAddr          string          `json:"dmsg_address,omitempty"`
	DmsgServers       []string        `json:"dmsg_servers,omitempty"`
	PublicAutoconnect bool            `json:"public_autoconnect,omitempty"`
	StcprCount        int             `json:"stcpr_count,omitempty"`
	SudphCount        int             `json:"sudph_count,omitempty"`
	NetworkTypes      []string        `json:"network_types,omitempty"`
}

// GetServiceHealth gets the response from the given service url
func GetServiceHealth(ctx context.Context, url string) (health *HealthCheckResponse, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url+path, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
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
