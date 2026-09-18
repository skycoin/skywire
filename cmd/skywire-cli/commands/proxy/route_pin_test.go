// Package skysocksc cmd/skywire-cli/commands/proxy/route_pin_test.go c4-app-proxy
package skysocksc

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/visor"
)

// pinStub is a visor whose route group appears only after `coldTicks` reads —
// the cold start, where `proxy start --route` returns from its poll loop the
// moment the app stamps Running and the group is not queryable yet.
type pinStub struct {
	coldTicks int
	reads     int
	legs      []string
	addErr    error
	adds      int
	removes   int
}

func (s *pinStub) RouteGroupMuxInfo(string) ([]visor.MuxRouteGroupInfo, error) {
	s.reads++
	if s.reads <= s.coldTicks {
		return nil, nil // no group yet: selectAutoRG refuses, pinRoutes waits
	}
	rg := visor.MuxRouteGroupInfo{}
	for i, tp := range s.legs {
		rg.Legs = append(rg.Legs, visor.MuxLegInfo{Index: i, TransportID: tp})
	}
	return []visor.MuxRouteGroupInfo{rg}, nil
}

func (s *pinStub) AddMuxRoute(_ string, fwd, _ []routing.Hop, _ uint16) error {
	s.adds++
	if s.addErr != nil {
		return s.addErr
	}
	s.legs = append(s.legs, fwd[0].TpID.String())
	return nil
}

func (s *pinStub) RemoveMuxRoute(_ string, tpID uuid.UUID, _ uint16) error {
	s.removes++
	for i, tp := range s.legs {
		if tp == tpID.String() {
			s.legs = append(s.legs[:i], s.legs[i+1:]...)
			break
		}
	}
	return nil
}

func pinTarget(t *testing.T, tp uuid.UUID) routePair {
	t.Helper()
	hop := routing.Hop{TpID: tp}
	return routePair{Forward: []routing.Hop{hop}, Reverse: []routing.Hop{hop}}
}

// The cold path: the group is not queryable for the first few reads, then it
// is. The pin must land, and the wait must SAY it is waiting — a start killed
// from outside (the bench's own `timeout`) printed "Running!" and nothing else,
// which read as a finished start on a session that had no legs at all.
func TestPinRoutesWaitsForAColdRouteGroupAndSaysSo(t *testing.T) {
	want := uuid.New()
	s := &pinStub{coldTicks: 3, legs: []string{uuid.New().String()}}
	var out bytes.Buffer

	res, err := pinRoutes(s, "skysocks-client-ref", []routePair{pinTarget(t, want)}, 30*time.Second, &out)
	require.NoError(t, err)
	require.Equal(t, []string{want.String()}, res.added)
	require.Len(t, res.removed, 1, "the auto leg is pruned so the session runs on the pin alone")
	require.Equal(t, []string{want.String()}, s.legs)
	require.Contains(t, out.String(), "waiting for skysocks-client-ref's route group")
	require.Equal(t, 1, bytes.Count(out.Bytes(), []byte("waiting for")), "the wait says so once, not per poll")
}

// A group that never becomes queryable ends in a named failure inside the
// budget, instead of an unbounded silence.
func TestPinRoutesFailsLoudlyWhenNoGroupEverAppears(t *testing.T) {
	s := &pinStub{coldTicks: 1 << 30}
	var out bytes.Buffer

	_, err := pinRoutes(s, "skysocks-client-ref", []routePair{pinTarget(t, uuid.New())}, 1200*time.Millisecond, &out)
	require.Error(t, err)
	require.Contains(t, err.Error(), "no route group to pin")
	require.Contains(t, out.String(), "waiting for")
}

// The regression the bench caught: reconcileLegs logs a failed AddMuxRoute to
// stderr and returns no error, so a start whose pinned leg never installed used
// to print "route pinned: 0 leg(s) added" and exit 0 — on a session running the
// auto route the operator had explicitly replaced. A pin that is not on the
// group is now an error.
func TestPinRoutesFailsWhenThePinnedLegDidNotInstall(t *testing.T) {
	s := &pinStub{legs: []string{uuid.New().String()}, addErr: errors.New("route setup timed out")}
	var out bytes.Buffer

	_, err := pinRoutes(s, "skysocks-client-ref", []routePair{pinTarget(t, uuid.New())}, 5*time.Second, &out)
	require.Error(t, err)
	require.Contains(t, err.Error(), "NOT running on the pinned route")
	require.Equal(t, 1, s.adds)
}
