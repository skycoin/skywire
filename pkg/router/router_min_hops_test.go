// Package router pkg/router/router_min_hops_test.go — the visor-global
// min-hops setting is part of every dial's constraint, not a default that a
// caller passing no options overrides.
//
// Reported from mobile as the VPN refusing to connect at min_hops 2 and 3
// while 1 worked. Investigating it turned up the opposite defect underneath:
// the setting was being bypassed. DialOptions.MinHops == 0 means "inherit
// Config.MinHops" (see its doc), but three sites read that zero as "no
// constraint" — the direct-transport downgrade in DialRoutes, appnet's direct
// shortcut, and the local-BFS fallback. A VPN client, which passes no per-dial
// options at all, therefore got a single direct hop whenever a direct
// transport happened to exist, with min_hops set to 3 and nothing said.
//
// EffectiveMinHops is the one resolver those sites now share, so these cases
// are what "the operator's setting applies" means in one place.
package router

import (
	"errors"
	"strings"
	"testing"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
)

func TestRouter_EffectiveMinHops(t *testing.T) {
	cases := []struct {
		name   string
		global uint16
		opts   *DialOptions
		want   uint16
	}{
		{"nil opts inherits the global setting", 3, nil, 3},
		{
			"no per-dial constraint inherits the global setting — the VPN client's case",
			3, &DialOptions{}, 3,
		},
		{"per-dial wins when stricter", 1, &DialOptions{MinHops: 4}, 4},
		{
			"global wins when stricter — a per-dial 2 cannot loosen an operator's 3",
			3, &DialOptions{MinHops: 2}, 3,
		},
		{"forward override counts", 1, &DialOptions{ForwardMinHops: 3}, 3},
		{"reverse override counts", 1, &DialOptions{ReverseMinHops: 3}, 3},
		{"routing disabled stays disabled", 0, &DialOptions{}, 0},
		{"unset global, unset opts", 1, &DialOptions{}, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := &router{conf: &Config{MinHops: c.global}}
			if got := r.EffectiveMinHops(c.opts); got != c.want {
				t.Errorf("EffectiveMinHops = %d, want %d (global=%d, opts=%+v)",
					got, c.want, c.global, c.opts)
			}
		})
	}
}

// The gate the direct-transport downgrade and appnet's shortcut both ask.
// Stated as its own case because it is the whole bug: with a global of 2 or
// more and no per-dial options, "is a direct hop acceptable" must be no.
func TestRouter_GlobalMinHopsForbidsDirect(t *testing.T) {
	for _, global := range []uint16{2, 3, 4} {
		r := &router{conf: &Config{MinHops: global}}
		if r.EffectiveMinHops(nil) <= 1 {
			t.Errorf("min_hops=%d allowed a direct dial: the constraint is bypassed "+
				"before route setup and the operator is not told", global)
		}
		if r.EffectiveMinHops(&DialOptions{}) <= 1 {
			t.Errorf("min_hops=%d with empty opts allowed a direct dial — this is the "+
				"path a VPN client takes", global)
		}
	}
	// And one hop stays allowed when nothing asks for more, or every dial
	// that has no route through intermediates starts failing.
	r := &router{conf: &Config{MinHops: 1}}
	if r.EffectiveMinHops(&DialOptions{}) > 1 {
		t.Error("min_hops=1 must still permit a direct dial")
	}
}

func TestRouter_SetMinHopIsReadBack(t *testing.T) {
	r := &router{conf: &Config{MinHops: 1}, logger: logging.MustGetLogger("test_min_hops")}
	r.SetMinHop(3)
	if got := r.MinHops(); got != 3 {
		t.Errorf("MinHops() = %d after SetMinHop(3), want 3", got)
	}
	if got := r.EffectiveMinHops(&DialOptions{}); got != 3 {
		t.Errorf("a dial after SetMinHop(3) resolved to %d — the change did not reach dials", got)
	}
}

// The message a user gets when the hop count is what failed. "no route founds"
// alone never mentions hops, so the setting they chose is the last thing they
// think to change.
func TestNoRouteErr_NamesTheConstraint(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()

	plain := noRouteErr(1, 1, pk)
	if !errors.Is(plain, ErrNoRouteFound) {
		t.Fatal("a 1-hop failure must still be ErrNoRouteFound")
	}
	if plain.Error() != ErrNoRouteFound.Error() {
		t.Errorf("a 1-hop failure gained hop wording it cannot explain: %q", plain.Error())
	}

	constrained := noRouteErr(3, 3, pk)
	if !errors.Is(constrained, ErrNoRouteFound) {
		t.Error("the constrained error must still satisfy errors.Is(ErrNoRouteFound) — " +
			"callers match on it")
	}
	msg := constrained.Error()
	for _, want := range []string{"min_hops=3", "2 intermediates", pk.String()} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q does not mention %q", msg, want)
		}
	}

	// Asymmetric: the stricter direction is the one that could not be served.
	if msg := noRouteErr(1, 4, pk).Error(); !strings.Contains(msg, "min_hops=4") {
		t.Errorf("asymmetric error reported the looser direction: %q", msg)
	}

	// One intermediate reads as one, not "1 intermediates".
	if msg := noRouteErr(2, 2, pk).Error(); !strings.Contains(msg, "1 intermediate ") &&
		!strings.Contains(msg, "1 intermediate)") {
		t.Errorf("expected a singular intermediate in %q", msg)
	}
}
