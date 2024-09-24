package store

import (
	"math"
	"net"

	"github.com/skycoin/skywire-utilities/pkg/geo"
	"github.com/skycoin/skywire-utilities/pkg/logging"
)

var log = logging.MustGetLogger("uptime_store")

// VisorsResponse is the tracker API response format for `/visors`.
type VisorsResponse []VisorDef

// VisorDef is the item of `VisorsResponse`.
type VisorDef struct {
	Lat float64 `json:"lat"`
	Lon float64 `json:"lon"`
}

func makeVisorsResponse(ips map[string]string, locDetails geo.LocationDetails) (VisorsResponse, error) {
	response := VisorsResponse{}
	for pk, ip := range ips {
		geo, err := locDetails(net.ParseIP(ip))
		if err != nil {
			log.WithError(err).
				WithField("ip", ip).
				WithField("pk", pk).
				Errorln("Failed to get IP location")
			continue
		}

		response = append(response, VisorDef{
			Lat: math.Round(geo.Lat*100) / 100,
			Lon: math.Round(geo.Lon*100) / 100,
		})
	}

	return response, nil
}
