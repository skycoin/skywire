// Package servicedisc pkg/servicedisc/query.go
package servicedisc

import (
	"fmt"
	"net/url"
	"strconv"
)

// GeoQuery represents query values for a proxies by geo call.
type GeoQuery struct {
	Lat        float64
	Lon        float64
	Radius     float64 // Format: <value><unit>
	RadiusUnit string
	Count      int64
}

// DefaultGeoQuery returns GeoQuery with default values.
func DefaultGeoQuery() GeoQuery {
	return GeoQuery{
		Lat:        0,
		Lon:        0,
		Radius:     2000,
		RadiusUnit: "km",
		Count:      1000,
	}
}

// Fill fills GeoQuery with query values.
func (q *GeoQuery) Fill(v url.Values) error {
	if latS := v.Get("lat"); latS != "" {
		lat, err := strconv.ParseFloat(latS, 64)
		if err != nil {
			return fmt.Errorf("invalid 'lat' query: %w", err)
		}
		q.Lat = lat
	}
	if lonS := v.Get("lon"); lonS != "" {
		lon, err := strconv.ParseFloat(lonS, 64)
		if err != nil {
			return fmt.Errorf("invalid 'lon' query: %w", err)
		}
		q.Lon = lon
	}
	if radS := v.Get("rad"); radS != "" {
		rad, err := strconv.ParseFloat(radS, 64)
		if err != nil {
			return fmt.Errorf("invalid 'radius' query: %w", err)
		}
		q.Radius = rad
	}
	if unit := v.Get("radUnit"); unit != "" {
		switch unit {
		case "m", "km", "mi", "ft":
			q.RadiusUnit = unit
		default:
			return fmt.Errorf("invalid 'radUnit' query: valid values include [%v]",
				[]string{"m", "km", "mi", "ft"})
		}
	}
	if countS := v.Get("count"); countS != "" {
		count, err := strconv.ParseInt(countS, 10, 64)
		if err != nil {
			return fmt.Errorf("invalid 'count' query: %w", err)
		}
		if count < 0 {
			count = 0
		}
		q.Count = count
	}
	return nil
}

// ServicesQuery represents query values for a proxies call.
type ServicesQuery struct {
	Count  int64  // <=0 : no limit
	Cursor uint64 // <=0 : 0 offset
}

// Fill fills ServicesQuery with query values.
func (q *ServicesQuery) Fill(v url.Values) error {
	if countS := v.Get("count"); countS != "" {
		count, err := strconv.ParseInt(countS, 10, 64)
		if err != nil {
			return fmt.Errorf("invalid 'count' query: %w", err)
		}
		if count < 0 {
			count = 0
		}
		q.Count = count
	}
	if cursorS := v.Get("cursor"); cursorS != "" {
		cursor, err := strconv.ParseUint(cursorS, 10, 64)
		if err != nil {
			return fmt.Errorf("invalid 'cursor' query: %w", err)
		}
		q.Cursor = cursor
	}
	return nil
}
