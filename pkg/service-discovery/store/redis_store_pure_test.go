// Package store — redis_store_pure_test.go: unit tests for the redis
// store's pure helpers (key builders, timeline-slot math, error mapping)
// that don't touch the redis client. The redis-backed methods are
// exercised by the gated integration test in redis_store_integration_test.go.
package store

import (
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/servicedisc"
)

func TestKeyBuilders(t *testing.T) {
	s := &redisStore{} // these helpers don't use the client
	pk, _ := cipher.GenerateKeyPair()
	addr := servicedisc.NewSWAddr(pk, 44)

	assert.Equal(t, "service:vpn:"+pk.String(), s.serviceKey("vpn", addr))
	assert.Equal(t, "service:vpn", s.serviceTypeSetKey("vpn"))
	assert.Equal(t, "sd:visor-svc:"+pk.Hex(), s.visorServicesKey(pk.Hex()))
}

func TestUptimeKeyBuilders(t *testing.T) {
	const date = "2026-06-18"
	pk := "0277e8e0c0e0"

	assert.Equal(t, "sd:uptime:"+pk+":"+date, sdUptimeKey(pk, date))
	assert.Equal(t, "sd:uptime:online:"+date, sdUptimeOnlineKey(date))
	assert.Equal(t, "sd:uptime:"+pk+":"+date+":timeline", sdUptimeTimelineKey(pk, date))
}

func TestCurrentTimelineSlot(t *testing.T) {
	cases := []struct {
		h, m int
		want int64
	}{
		{0, 0, 0},     // first slot of the day
		{0, 4, 0},     // still slot 0 (5-min buckets)
		{0, 5, 1},     // second slot
		{1, 0, 12},    // 1h = 12 slots
		{23, 55, 287}, // last slot of the day
	}
	for _, c := range cases {
		got := currentTimelineSlot(time.Date(2026, 6, 18, c.h, c.m, 0, 0, time.UTC))
		assert.Equal(t, c.want, got, "h=%d m=%d", c.h, c.m)
	}
}

func TestProcessErr(t *testing.T) {
	s := &redisStore{}

	// nil error → nil result.
	assert.Nil(t, s.processErr(nil, http.StatusInternalServerError))

	// non-nil error → HTTPError carrying the status + message.
	herr := s.processErr(errors.New("boom"), http.StatusBadGateway)
	assert.NotNil(t, herr)
	assert.Equal(t, http.StatusBadGateway, herr.HTTPStatus)
	assert.Equal(t, "boom", herr.Err)
}
