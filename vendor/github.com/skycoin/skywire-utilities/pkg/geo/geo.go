// Package geo pkg/geo/geo.go
package geo

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net"
	"net/http"

	"github.com/sirupsen/logrus"
	"github.com/skycoin/skywire-utilities/pkg/logging"

	"github.com/skycoin/skywire-utilities/pkg/netutil"
)

// Errors associated with geo calls.
var (
	ErrIPIsNotPublic         = errors.New("ip address is not public")
	ErrCannotObtainLocFromIP = errors.New("cannot obtain location from IP")
)

const (
	reqURL = "http://ip.skycoin.com/?ip=%s"
)

// LocationData represents a geolocation point.
type LocationData struct {
	Lat     float64 `json:"lat,omitempty"`
	Lon     float64 `json:"lon,omitempty"`
	Country string  `json:"country,omitempty"`
	Region  string  `json:"region,omitempty"`
}

// LocationDetails represents a function that obtains geolocation from a given IP.
type LocationDetails func(ip net.IP) (*LocationData, error)

// MakeIPDetails returns a GeoFunc.
func MakeIPDetails(log logrus.FieldLogger, apiKey string) LocationDetails {
	// Just in case.
	if log == nil {
		log = logging.MustGetLogger("geo")
	}

	return func(ip net.IP) (*LocationData, error) {
		// Check if IP is public IP.
		if !netutil.IsPublicIP(ip) {
			return nil, ErrIPIsNotPublic
		}

		// Get Geo from IP.
		var (
			resp *http.Response
			err  error
		)

		resp, err = http.Get(fmt.Sprintf(reqURL, ip.String()))
		if err != nil {
			return nil, err
		}
		defer func() { _ = resp.Body.Close() }() //nolint:errcheck

		// Get body.
		j := struct {
			CountryCode string  `json:"country_code"`
			Region      string  `json:"region_code"`
			Lat         float64 `json:"latitude"`
			Lon         float64 `json:"longitude"`
		}{}
		if err := json.NewDecoder(resp.Body).Decode(&j); err != nil {
			return nil, err
		}
		if j.CountryCode == "" && j.Region == "" && j.Lat == 0 && j.Lon == 0 {
			return nil, fmt.Errorf("call to ip.skycoin.com returned empty: %s", ErrCannotObtainLocFromIP)
		}

		// Prepare output.
		out := LocationData{
			Lat:     roundTwoDigits(j.Lat),
			Lon:     roundTwoDigits(j.Lon),
			Country: j.CountryCode,
			Region:  j.Region,
		}
		log.WithField("geo", out).Info()

		return &out, nil
	}
}

func roundTwoDigits(value float64) float64 {
	return math.Round(value*100) / 100
}
