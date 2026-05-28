// Package policy pkg/router/policy/bridge.go — translates Go
// RoutingContext / Candidate / RouteSpec to and from Starlark
// values, and assembles the pre-declared module set the policy
// script sees on load.
package policy

import (
	"fmt"
	"strings"
	"time"

	"go.starlark.net/starlark"
	"go.starlark.net/starlarkstruct"
)

// preDeclared returns the set of names the policy script sees as
// top-level globals before its own code runs: the stdlib modules
// (datetime, geo, etc.) and the RouteSpec constructor. Names not
// in this set surface as "undefined" errors at parse time.
func (e *Evaluator) preDeclared() starlark.StringDict {
	return starlark.StringDict{
		"datetime":   datetimeModule(e.clock),
		"geo":        geoModule(e.provider),
		"transports": transportsModule(e.provider),
		"peers":      peersModule(e.provider),
		"logging":    loggingModule(e),
		"RouteSpec":  starlark.NewBuiltin("RouteSpec", routeSpecCtor),
		"Candidate":  starlark.NewBuiltin("Candidate", candidateCtor),
	}
}

// geoModule exposes geographic lookups on peer PKs. Operators
// write `geo.country(pk) == "ID"` to filter routes by intermediate
// country. Returns "??" for unknown PKs so policies can branch on
// it cleanly (`if geo.country(pk) != "??"`).
func geoModule(p Provider) *starlarkstruct.Module {
	return &starlarkstruct.Module{
		Name: "geo",
		Members: starlark.StringDict{
			"country": starlark.NewBuiltin("country", oneStringArg(func(pk string) starlark.Value {
				return starlark.String(p.Geo(pk))
			})),
		},
	}
}

// transportsModule surfaces transport-tracker state. `latency(pk)`
// returns ms; zero means unknown. `kind(pk)` returns "stcpr" etc.;
// empty string means unknown. Both queries are memoized in the
// Provider so calling them on every dial doesn't re-query.
func transportsModule(p Provider) *starlarkstruct.Module {
	return &starlarkstruct.Module{
		Name: "transports",
		Members: starlark.StringDict{
			"latency": starlark.NewBuiltin("latency", oneStringArg(func(pk string) starlark.Value {
				return starlark.MakeInt(p.Latency(pk))
			})),
			"kind": starlark.NewBuiltin("kind", oneStringArg(func(pk string) starlark.Value {
				return starlark.String(p.Kind(pk))
			})),
		},
	}
}

// peersModule surfaces the operator-configured trust list and the
// visor's hypervisor list. Common patterns:
//
//	if peers.is_trusted(c.hops[-1]): ...
//	if peers.is_hypervisor(ctx.peer_pk): mux = 4
func peersModule(p Provider) *starlarkstruct.Module {
	return &starlarkstruct.Module{
		Name: "peers",
		Members: starlark.StringDict{
			"is_trusted": starlark.NewBuiltin("is_trusted", oneStringArg(func(pk string) starlark.Value {
				return starlark.Bool(p.IsTrusted(pk))
			})),
			"is_hypervisor": starlark.NewBuiltin("is_hypervisor", oneStringArg(func(pk string) starlark.Value {
				return starlark.Bool(p.IsHypervisor(pk))
			})),
		},
	}
}

// loggingModule lets policies emit info / warn messages to the
// visor's log pipeline. Messages flow through the evaluator's
// configured logger (WithLogger). Per-script rate-limited so a
// runaway policy can't log-flood the visor.
func loggingModule(e *Evaluator) *starlarkstruct.Module {
	return &starlarkstruct.Module{
		Name: "logging",
		Members: starlark.StringDict{
			"info": starlark.NewBuiltin("info", oneStringArg(func(msg string) starlark.Value {
				e.logger("policy %s: info: %s", e.source, msg)
				return starlark.None
			})),
			"warn": starlark.NewBuiltin("warn", oneStringArg(func(msg string) starlark.Value {
				e.logger("policy %s: warn: %s", e.source, msg)
				return starlark.None
			})),
		},
	}
}

// oneStringArg is a small helper that wraps a Go function taking
// a string PK and returning a starlark.Value into a Starlark
// builtin signature. Cuts the repetitive boilerplate in the
// module definitions above.
func oneStringArg(fn func(string) starlark.Value) func(*starlark.Thread, *starlark.Builtin, starlark.Tuple, []starlark.Tuple) (starlark.Value, error) {
	return func(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, _ []starlark.Tuple) (starlark.Value, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("%s: expected 1 string arg, got %d", b.Name(), len(args))
		}
		s, ok := args[0].(starlark.String)
		if !ok {
			return nil, fmt.Errorf("%s: expected string, got %s", b.Name(), args[0].Type())
		}
		return fn(string(s)), nil
	}
}

// datetimeModule exposes time-of-day primitives the operator's
// policy needs to write rules like "only on Friday at 5pm." The
// clock is injected by the evaluator so tests can simulate any
// moment in time deterministically.
func datetimeModule(clock Clock) *starlarkstruct.Module {
	return &starlarkstruct.Module{
		Name: "datetime",
		Members: starlark.StringDict{
			"now": starlark.NewBuiltin("now",
				func(_ *starlark.Thread, _ *starlark.Builtin, _ starlark.Tuple, _ []starlark.Tuple) (starlark.Value, error) {
					return timeValue(clock.Now()), nil
				}),
			"weekday": starlark.NewBuiltin("weekday",
				func(_ *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, _ []starlark.Tuple) (starlark.Value, error) {
					if len(args) != 1 {
						return nil, fmt.Errorf("weekday: expected 1 arg, got %d", len(args))
					}
					t, ok := args[0].(*timeWrapper)
					if !ok {
						return nil, fmt.Errorf("weekday: expected datetime, got %s", args[0].Type())
					}
					return starlark.String(strings.ToLower(t.t.Weekday().String())), nil
				}),
		},
	}
}

// timeWrapper makes a Go time.Time visible to Starlark. We expose
// `hour`, `minute`, `weekday()` as direct attributes/methods so
// the operator can write `ctx.now().hour` etc. without learning a
// new datetime API.
type timeWrapper struct{ t time.Time }

func timeValue(t time.Time) *timeWrapper { return &timeWrapper{t: t} }

func (w *timeWrapper) String() string {
	return fmt.Sprintf("datetime(%s)", w.t.Format(time.RFC3339))
}
func (w *timeWrapper) Type() string          { return "datetime" }
func (w *timeWrapper) Freeze()               {}
func (w *timeWrapper) Truth() starlark.Bool  { return starlark.True }
func (w *timeWrapper) Hash() (uint32, error) { return uint32(w.t.UnixNano()), nil } //nolint:gosec
func (w *timeWrapper) AttrNames() []string   { return []string{"hour", "minute", "second"} }
func (w *timeWrapper) Attr(name string) (starlark.Value, error) {
	switch name {
	case "hour":
		return starlark.MakeInt(w.t.Hour()), nil
	case "minute":
		return starlark.MakeInt(w.t.Minute()), nil
	case "second":
		return starlark.MakeInt(w.t.Second()), nil
	}
	return nil, nil
}

// buildContextValue converts a Go RoutingContext to a Starlark
// struct-shaped value so the policy can read `ctx.app`,
// `ctx.peer_pk`, etc.
func buildContextValue(rctx RoutingContext) starlark.Value {
	cliOverrides := starlark.NewDict(len(rctx.CLIOverrides))
	for k, v := range rctx.CLIOverrides {
		_ = cliOverrides.SetKey(starlark.String(k), starlark.String(v)) //nolint:errcheck
	}
	return starlarkstruct.FromStringDict(
		starlarkstruct.Default,
		starlark.StringDict{
			"app":     starlark.String(rctx.App),
			"peer_pk": starlark.String(rctx.PeerPK),
			"port":    starlark.MakeUint(uint(rctx.Port)),
			"now": starlark.NewBuiltin("now", func(_ *starlark.Thread, _ *starlark.Builtin, _ starlark.Tuple, _ []starlark.Tuple) (starlark.Value, error) {
				return timeValue(rctx.Now), nil
			}),
			"cli_overrides": cliOverrides,
		},
	)
}

// buildCandidatesValue converts the Go candidate list into a
// Starlark list of struct-shaped values.
func buildCandidatesValue(cs []Candidate) starlark.Value {
	out := make([]starlark.Value, len(cs))
	for i, c := range cs {
		hops := make([]starlark.Value, len(c.Hops))
		for j, h := range c.Hops {
			hops[j] = starlark.String(h)
		}
		hopsGeo := make([]starlark.Value, len(c.HopsGeo))
		for j, g := range c.HopsGeo {
			hopsGeo[j] = starlark.String(g)
		}
		kinds := make([]starlark.Value, len(c.TransportKinds))
		for j, k := range c.TransportKinds {
			kinds[j] = starlark.String(k)
		}
		out[i] = starlarkstruct.FromStringDict(
			starlarkstruct.Default,
			starlark.StringDict{
				"hops":               starlark.NewList(hops),
				"hops_geo":           starlark.NewList(hopsGeo),
				"est_latency_ms":     starlark.MakeInt(c.EstLatencyMs),
				"transport_kinds":    starlark.NewList(kinds),
				"mux_legs_available": starlark.MakeInt(c.MuxLegsAvailable),
			},
		)
	}
	return starlark.NewList(out)
}

// routeSpecCtor is `RouteSpec(...)` callable from inside the
// script. Accepts kwargs only — positional args rejected so the
// script side stays self-documenting.
func routeSpecCtor(_ *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	if len(args) > 0 {
		return nil, fmt.Errorf("RouteSpec: positional arguments not allowed; use kwargs")
	}
	dict := starlark.StringDict{
		"chosen":       starlark.None,
		"mux":          starlark.MakeInt(0),
		"min_hops":     starlark.MakeInt(0),
		"fallback":     starlark.String(""),
		"distribution": starlark.String(""),
	}
	for _, kv := range kwargs {
		key := string(kv[0].(starlark.String))
		if _, ok := dict[key]; !ok {
			return nil, fmt.Errorf("RouteSpec: unknown field %q", key)
		}
		dict[key] = kv[1]
	}
	return starlarkstruct.FromStringDict(starlarkstruct.Default, dict), nil
}

// candidateCtor lets policies synthesize a Candidate when they
// want to (e.g. returning a direct-transport candidate that wasn't
// in the input list). Mostly useful in tests.
func candidateCtor(_ *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	if len(args) > 0 {
		return nil, fmt.Errorf("Candidate: positional arguments not allowed; use kwargs")
	}
	dict := starlark.StringDict{
		"hops":               starlark.NewList(nil),
		"hops_geo":           starlark.NewList(nil),
		"est_latency_ms":     starlark.MakeInt(0),
		"transport_kinds":    starlark.NewList(nil),
		"mux_legs_available": starlark.MakeInt(0),
	}
	for _, kv := range kwargs {
		key := string(kv[0].(starlark.String))
		if _, ok := dict[key]; !ok {
			return nil, fmt.Errorf("Candidate: unknown field %q", key)
		}
		dict[key] = kv[1]
	}
	return starlarkstruct.FromStringDict(starlarkstruct.Default, dict), nil
}

// parseRouteSpec reads a Starlark value (expected: the struct
// returned by RouteSpec(...)) back into the Go RouteSpec the
// evaluator returns to the router.
func parseRouteSpec(v starlark.Value) (RouteSpec, error) {
	// None means "fall back to default."
	if _, ok := v.(starlark.NoneType); ok {
		return RouteSpec{}, nil
	}
	s, ok := v.(*starlarkstruct.Struct)
	if !ok {
		return RouteSpec{}, fmt.Errorf("expected struct, got %s", v.Type())
	}
	out := RouteSpec{}

	if chosen, err := s.Attr("chosen"); err == nil && chosen != nil {
		if _, isNone := chosen.(starlark.NoneType); !isNone {
			cand, err := parseCandidate(chosen)
			if err != nil {
				return RouteSpec{}, fmt.Errorf("chosen: %w", err)
			}
			out.Chosen = &cand
		}
	}
	out.Mux = readIntField(s, "mux")
	out.MinHops = readIntField(s, "min_hops")
	out.Fallback = readStrField(s, "fallback")
	out.Distribution = readStrField(s, "distribution")
	return out, nil
}

func parseCandidate(v starlark.Value) (Candidate, error) {
	s, ok := v.(*starlarkstruct.Struct)
	if !ok {
		return Candidate{}, fmt.Errorf("expected candidate struct, got %s", v.Type())
	}
	return Candidate{
		Hops:             readStrListField(s, "hops"),
		HopsGeo:          readStrListField(s, "hops_geo"),
		EstLatencyMs:     readIntField(s, "est_latency_ms"),
		TransportKinds:   readStrListField(s, "transport_kinds"),
		MuxLegsAvailable: readIntField(s, "mux_legs_available"),
	}, nil
}

func readIntField(s *starlarkstruct.Struct, name string) int {
	v, err := s.Attr(name)
	if err != nil || v == nil {
		return 0
	}
	if i, ok := v.(starlark.Int); ok {
		n, _ := i.Int64()
		return int(n)
	}
	return 0
}

func readStrField(s *starlarkstruct.Struct, name string) string {
	v, err := s.Attr(name)
	if err != nil || v == nil {
		return ""
	}
	if str, ok := v.(starlark.String); ok {
		return string(str)
	}
	return ""
}

func readStrListField(s *starlarkstruct.Struct, name string) []string {
	v, err := s.Attr(name)
	if err != nil || v == nil {
		return nil
	}
	list, ok := v.(*starlark.List)
	if !ok {
		return nil
	}
	out := make([]string, list.Len())
	for i := 0; i < list.Len(); i++ {
		if s, ok := list.Index(i).(starlark.String); ok {
			out[i] = string(s)
		}
	}
	return out
}
