package arfeed

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestPunchCompatible(t *testing.T) {
	cases := []struct {
		a, b string
		ok   bool
	}{
		{NATFullCone, NATSymmetric, true},
		{NATRestricted, NATSymmetric, true},
		{NATOpen, NATSymmetric, true},
		{NATPortRestricted, NATSymmetric, false},
		{NATUDPFirewall, NATSymmetric, false},
		{NATSymmetric, NATSymmetric, false},
		{NATPortRestricted, NATPortRestricted, true},
		{"", NATSymmetric, true},
		{NATBlocked, NATOpen, false},
		{"", "", true},
	}
	for _, c := range cases {
		require.Equal(t, c.ok, PunchCompatible(c.a, c.b), "%q/%q", c.a, c.b)
		require.Equal(t, c.ok, PunchCompatible(c.b, c.a), "%q/%q reversed", c.b, c.a)
	}
}

func TestVerdict(t *testing.T) {
	now := time.Now()
	var none *Reach
	require.Equal(t, Untested, none.Verdict(TypeSUDPH, "", now), "no record at all is unknown")

	// Probed listener types.
	pub := &Reach{Bound: []string{TypeSTCPR, TypeQUIC}, Open: []string{TypeSTCPR}, Closed: []string{TypeQUIC}}
	require.Equal(t, Confirmed, pub.Verdict(TypeSTCPR, "", now))
	require.Equal(t, Unreachable, pub.Verdict(TypeQUIC, "", now), "accepts stcpr, router drops UDP")
	require.Equal(t, Unreachable, pub.Verdict(TypeWT, "", now), "not bound")
	require.Equal(t, Untested, (&Reach{Bound: []string{TypeWT}}).Verdict(TypeWT, "", now), "probe pending")

	// sudph needs the live control connection and a compatible pair.
	s := &Reach{ReachDecl: ReachDecl{NAT: NATPortRestricted}, Bound: []string{TypeSUDPH}, Live: true}
	require.Equal(t, Capable, s.Verdict(TypeSUDPH, NATFullCone, now))
	require.Equal(t, Unreachable, s.Verdict(TypeSUDPH, NATSymmetric, now))
	stale := &Reach{Bound: []string{TypeSUDPH}}
	require.Equal(t, Unreachable, stale.Verdict(TypeSUDPH, NATFullCone, now), "binding without a live UDP connection")
	old := &Reach{Bound: []string{TypeSUDPH}, Live: true}
	require.Equal(t, Untested, old.Verdict(TypeSUDPH, NATFullCone, now), "live but states no NAT class")

	// A recent inbound confirmation outranks everything measured.
	s.Inbound = map[string]int64{TypeSUDPH: now.Add(-2 * time.Hour).Unix()}
	require.Equal(t, Confirmed, s.Verdict(TypeSUDPH, NATSymmetric, now))
	s.Inbound[TypeSUDPH] = now.Add(-3 * InboundRecent).Unix()
	require.Equal(t, Unreachable, s.Verdict(TypeSUDPH, NATSymmetric, now), "an old confirmation expires")

	// Declared accepts gate: a visor that states it does not serve a type.
	w := &Reach{ReachDecl: ReachDecl{NAT: NATFullCone, Accepts: []string{TypeSUDPH}}}
	require.Equal(t, Unreachable, w.Verdict(TypeWEBRTC, "", now))
	w.Accepts = append(w.Accepts, TypeWEBRTC)
	require.Equal(t, Capable, w.Verdict(TypeWEBRTC, NATRestricted, now))
	require.Equal(t, Unreachable, (&Reach{ReachDecl: ReachDecl{NAT: NATSymmetric, Accepts: []string{TypeWEBRTC}}}).
		Verdict(TypeWEBRTC, NATSymmetric, now))
}
