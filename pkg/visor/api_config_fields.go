// Package visor pkg/visor/api_config_fields.go c3-vis-core
//
// Field-level editing of the RUNNING visor's config.
//
// Editing skywire-config.json by hand while a visor runs does not
// work: the visor holds the config in memory and re-marshals the
// whole struct over the file on every setter that persists (see
// Visor.SetIsPublic, persistHypervisors, persistRouterKnobs, the
// launcher app updates, …). Whatever the operator typed into the
// file is erased at the next flush. The only durable edit is one
// the visor itself makes.
//
// SetConfigFields is that edit. It addresses a field by its dotted
// JSON path, validates the value against the visorconfig.V1 struct,
// and then either
//
//   - hands it to the live setter registered for that path
//     (liveConfigFields below), so the running subsystem picks it up
//     immediately, or
//   - writes it into v.conf and flushes, which takes effect on the
//     next start ("restart-required").
//
// Path resolution is reflection over V1's json tags rather than a
// hand-written field table: the config grows every release, and a
// table would be stale the week after it was written. The LIVE half
// is a table, because "a running subsystem can pick this up" is a
// property of the code, not of the struct — one place to register a
// new live setter (see liveConfigFields).
//
// Identity is not editable: sk/pk and every *_sk / *secret_key path
// is refused outright, matching SetRuntimeConfig. Maps another
// subsystem owns and rewrites wholesale (app_settings,
// routing.router_app_settings) are refused too, with a pointer at
// the command that does own them — persisting a value there would
// be silently overwritten by the owner's next flush.
package visor

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/router/routersettings"
	"github.com/skycoin/skywire/pkg/transport"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

// ConfigFieldChange reports one field's before/after from SetConfigFields.
type ConfigFieldChange struct {
	// Path is the dotted config path as the caller supplied it.
	Path string `json:"path"`
	// Old and New are the field's JSON values before and after.
	// Old is null when the path addressed an absent map key.
	Old json.RawMessage `json:"old"`
	New json.RawMessage `json:"new"`
	// Live is true when a running subsystem took the value; false
	// means it is on disk and applies at the next visor start.
	Live bool `json:"live"`
}

// String renders one change the way `config set` prints it.
func (c ConfigFieldChange) String() string {
	state := "restart-required"
	if c.Live {
		state = "live"
	}
	return fmt.Sprintf("%s: %s -> %s (%s)", c.Path, string(c.Old), string(c.New), state)
}

// ConfigFieldDoc is one row of the live-field table, for command help.
type ConfigFieldDoc struct {
	Path string `json:"path"`
	Desc string `json:"desc"`
}

// setConfigFieldsMu serializes SetConfigFields so two concurrent calls
// cannot interleave a partial apply with a flush.
var setConfigFieldsMu sync.Mutex

// liveConfigField is one config path a running visor can apply without a
// restart. apply receives the decoded value (already type-checked against
// the struct field) and the bracket index, if the path had one; it is
// responsible for persistence — every setter it delegates to flushes.
type liveConfigField struct {
	// Path is the registry template: the dotted path with "[*]" where
	// the caller supplies an index (an app name, a knob name).
	Path string
	// Desc is the one-line description shown in `config set --help`.
	Desc  string
	apply func(v *Visor, idx string, nv reflect.Value) error
}

// liveConfigFieldTable is THE list of config fields a running visor applies
// without a restart. Add a row here — and nowhere else — when a new live
// setter lands; `config set` and its help both read this table.
var liveConfigFieldTable = []liveConfigField{
	{
		Path: "is_public",
		Desc: "advertise this visor in service discovery (same path as `cli tp public`)",
		apply: func(v *Visor, _ string, nv reflect.Value) error {
			return v.SetIsPublic(nv.Bool())
		},
	}, {
		Path: "log_level",
		Desc: "visor log level (error|warn|info|debug|trace)",
		apply: func(v *Visor, _ string, nv reflect.Value) error {
			lvl, err := logging.LevelFromString(nv.String())
			if err != nil {
				return fmt.Errorf("invalid log level %q: %w", nv.String(), err)
			}
			if ml := v.conf.MasterLogger(); ml != nil {
				ml.SetLevel(lvl)
			}
			v.conf.LogLevel = nv.String()
			return v.conf.Flush()
		},
	}, {
		Path: "hypervisors",
		Desc: "configured hypervisor PKs (connects/disconnects the difference)",
		apply: func(v *Visor, _ string, nv reflect.Value) error {
			want, ok := nv.Interface().([]cipher.PubKey)
			if !ok {
				return errors.New("hypervisors must be a list of public keys")
			}
			return v.setHypervisorsLive(want)
		},
	}, {
		Path: "persistent_transports",
		Desc: "transports the visor re-dials until they exist",
		apply: func(v *Visor, _ string, nv reflect.Value) error {
			pts, ok := nv.Interface().([]transport.PersistentTransports)
			if !ok {
				return errors.New("persistent_transports must be a list of {pk, type}")
			}
			return v.SetPersistentTransports(pts)
		},
	}, {
		Path: "transport.public_autoconnect",
		Desc: "auto-dial transports to public visors",
		apply: func(v *Visor, _ string, nv reflect.Value) error {
			return v.SetPublicAutoconnect(nv.Bool())
		},
	}, {
		Path: "routing.min_hops",
		Desc: "minimum intermediate hops per route",
		apply: func(v *Visor, _ string, nv reflect.Value) error {
			return v.SetMinHops(uint16(nv.Uint())) //nolint:gosec
		},
	}, {
		Path: "routing.calculate_routes",
		Desc: "calculate routes locally instead of asking the route finder",
		apply: func(v *Visor, _ string, nv reflect.Value) error {
			return v.SetCalculateRoutes(nv.Bool())
		},
	}, {
		Path: "routing.transport_preference",
		Desc: "transport-type priority order, most-preferred first",
		apply: func(v *Visor, _ string, nv reflect.Value) error {
			order, ok := nv.Interface().([]string)
			if !ok {
				return errors.New("transport_preference must be a list of transport type names")
			}
			return v.SetTransportPreference(order)
		},
	}, {
		Path: "routing.mux_fec",
		Desc: "forward error correction on mux route groups",
		apply: func(v *Visor, _ string, nv reflect.Value) error {
			if v.router == nil {
				return errors.New("router not available")
			}
			v.router.SetMuxFEC(nv.Bool())
			v.conf.Routing.MuxFEC = nv.Bool()
			return v.conf.Flush()
		},
	}, {
		Path: "routing.router_settings[*]",
		Desc: "one router knob from the live catalog (see `cli route settings`)",
		apply: func(v *Visor, idx string, nv reflect.Value) error {
			if err := routersettings.ApplyApp("", map[string]string{idx: nv.String()}); err != nil {
				return err
			}
			return v.persistRouterKnobs()
		},
	}, {
		Path: "launcher.apps[*].auto_start",
		Desc: "start the named app when the visor starts",
		apply: func(v *Visor, idx string, nv reflect.Value) error {
			return v.SetAutoStart(idx, nv.Bool())
		},
	}, {
		Path: "launcher.apps[*].args",
		Desc: "replace the named app's argument list",
		apply: func(v *Visor, idx string, nv reflect.Value) error {
			args, ok := nv.Interface().([]string)
			if !ok {
				return errors.New("args must be a list of strings")
			}
			return v.SetAppArgs(idx, args)
		},
	}, {
		Path: "launcher.apps[*].env",
		Desc: "replace the named app's environment (KEY=VALUE entries)",
		apply: func(v *Visor, idx string, nv reflect.Value) error {
			env, ok := nv.Interface().([]string)
			if !ok {
				return errors.New("env must be a list of KEY=VALUE strings")
			}
			return v.SetAppEnvFull(idx, env)
		},
	}, {
		Path: "reward_address",
		Desc: "skycoin address rewards are paid to (durable store is <local_path>/reward.txt, not this json)",
		apply: func(v *Visor, _ string, nv reflect.Value) error {
			_, err := v.SetRewardAddress(nv.String())
			return err
		},
	}, {
		Path: "hypervisor.enable",
		Desc: "run the hypervisor: DMSG-RPC listener, managed-visor tracking and the web UI",
		apply: func(v *Visor, _ string, nv reflect.Value) error {
			if nv.Bool() {
				return v.EnableHypervisorPersist(true)
			}
			return v.DisableHypervisorPersist(true)
		},
	}, {
		Path: "hypervisor.ui_disable",
		Desc: "stop the hypervisor web UI, keeping DMSG-RPC, tracking and `hv ls`",
		apply: func(v *Visor, _ string, nv reflect.Value) error {
			if nv.Bool() {
				return v.DisableHypervisorUIPersist(true)
			}
			return v.EnableHypervisorUIPersist(true)
		},
	}, {
		Path: "hypervisor.enable_auth",
		Desc: "require a login on the hypervisor web UI (rebuilds the UI router; open tabs must reload)",
		apply: func(v *Visor, _ string, nv reflect.Value) error {
			return v.SetHypervisorAuthPersist(nv.Bool(), true)
		},
	}, {
		Path: "dmsg_web.enable",
		Desc: "run the .dmsg resolving proxy",
		apply: func(v *Visor, _ string, nv reflect.Value) error {
			return v.setEmbeddedProxyEnabledPersist("dmsg", nv.Bool())
		},
	}, {
		Path: "dmsg_web.proxy_addr",
		Desc: "SOCKS5 bind address for the .dmsg resolving proxy — \"\" is loopback (also enables it)",
		apply: func(v *Visor, _ string, nv reflect.Value) error {
			return v.SetEmbeddedProxyBind("dmsg", nv.String())
		},
	}, {
		Path: "dmsg_web.upstream_socks",
		Desc: "SOCKS5 upstream the .dmsg proxy forwards non-matching CONNECTs to",
		apply: func(v *Visor, _ string, nv reflect.Value) error {
			return v.setEmbeddedProxyUpstreamPersist("dmsg", nv.String())
		},
	}, {
		Path: "skynet_web.enable",
		Desc: "run the .skynet resolving proxy",
		apply: func(v *Visor, _ string, nv reflect.Value) error {
			return v.setEmbeddedProxyEnabledPersist("skynet", nv.Bool())
		},
	}, {
		Path: "skynet_web.proxy_addr",
		Desc: "SOCKS5 bind address for the .skynet resolving proxy — \"\" is loopback (also enables it)",
		apply: func(v *Visor, _ string, nv reflect.Value) error {
			return v.SetEmbeddedProxyBind("skynet", nv.String())
		},
	}, {
		Path: "skynet_web.upstream_socks",
		Desc: "SOCKS5 upstream the .skynet proxy forwards non-matching CONNECTs to",
		apply: func(v *Visor, _ string, nv reflect.Value) error {
			return v.setEmbeddedProxyUpstreamPersist("skynet", nv.String())
		},
	},
}

// liveConfigFieldByPath indexes the table by template path.
var liveConfigFieldByPath = func() map[string]liveConfigField {
	m := make(map[string]liveConfigField, len(liveConfigFieldTable))
	for _, f := range liveConfigFieldTable {
		m[f.Path] = f
	}
	return m
}()

// LiveConfigFields returns the config paths a running visor applies without a
// restart, sorted by path. `skywire cli config set --help` renders this, so the
// documented list and the implemented list cannot drift.
func LiveConfigFields() []ConfigFieldDoc {
	out := make([]ConfigFieldDoc, 0, len(liveConfigFieldTable))
	for _, f := range liveConfigFieldTable {
		out = append(out, ConfigFieldDoc{Path: f.Path, Desc: f.Desc})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// ownedConfigPaths are config subtrees another subsystem rewrites wholesale on
// its own flush. A value written here by `config set` would be silently dropped
// the next time the owner persists, so the write is refused with a pointer at
// the command that does own it.
var ownedConfigPaths = map[string]string{
	"app_settings":                   "skywire cli proxy settings",
	"app_settings[*]":                "skywire cli proxy settings",
	"routing.router_settings":        "skywire cli route settings",
	"routing.router_app_settings":    "skywire cli route settings --app <app>",
	"routing.router_app_settings[*]": "skywire cli route settings --app <app>",
}

// SetConfigFields validates every requested field against the visorconfig.V1
// struct and then applies them: live where a live setter is registered for the
// path, otherwise into the config file for the next start. Validation is done
// for ALL fields before ANY is applied, so a typo in the third argument does
// not leave the first two half-applied.
//
// Returns one ConfigFieldChange per field, sorted by path.
func (v *Visor) SetConfigFields(fields map[string]json.RawMessage) ([]ConfigFieldChange, error) {
	if v.conf == nil {
		return nil, errors.New("visor has no current config")
	}
	if len(fields) == 0 {
		return nil, errors.New("no fields given")
	}

	setConfigFieldsMu.Lock()
	defer setConfigFieldsMu.Unlock()

	paths := make([]string, 0, len(fields))
	for p := range fields {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	root := reflect.ValueOf(v.conf).Elem()

	// Phase 1 — resolve and decode everything. Nothing is mutated here.
	type pending struct {
		target *configTarget
		newVal reflect.Value
		change ConfigFieldChange
	}
	plan := make([]pending, 0, len(paths))
	needFlush := false
	for _, p := range paths {
		tgt, err := resolveConfigTarget(root, p)
		if err != nil {
			return nil, err
		}
		if owner, owned := ownedConfigPaths[tgt.tmpl]; owned {
			return nil, fmt.Errorf("%s is owned by the router/app settings catalogs; set it with `%s`", p, owner)
		}
		nv, err := decodeConfigValue(tgt.typ, fields[p])
		if err != nil {
			return nil, fmt.Errorf("%s: %w", p, err)
		}
		oldJSON, err := marshalConfigValue(tgt.get())
		if err != nil {
			return nil, fmt.Errorf("%s: read current value: %w", p, err)
		}
		newJSON, err := json.Marshal(nv.Interface())
		if err != nil {
			return nil, fmt.Errorf("%s: %w", p, err)
		}
		_, isLive := liveConfigFieldByPath[tgt.tmpl]
		if !isLive {
			needFlush = true
		}
		plan = append(plan, pending{
			target: tgt,
			newVal: nv,
			change: ConfigFieldChange{Path: p, Old: oldJSON, New: newJSON, Live: isLive},
		})
	}

	// A restart-required field is only meaningful if it can be written down.
	if needFlush && v.conf.Path() == "" {
		return nil, errors.New("visor config is not file-backed (STDIN or in-memory); refusing to write")
	}

	// Phase 2 — apply. Restart-required fields land in v.conf first and are
	// flushed once; the live setters flush themselves as they always have.
	out := make([]ConfigFieldChange, 0, len(plan))
	for _, p := range plan {
		if !p.change.Live {
			p.target.set(p.newVal)
		}
	}
	if needFlush {
		if err := v.conf.Flush(); err != nil {
			return nil, fmt.Errorf("flush config: %w", err)
		}
	}
	for _, p := range plan {
		if p.change.Live {
			f := liveConfigFieldByPath[p.target.tmpl]
			if err := f.apply(v, p.target.idx, p.newVal); err != nil {
				return out, fmt.Errorf("%s: %w", p.change.Path, err)
			}
		}
		if v.log != nil {
			v.log.WithField("path", p.change.Path).
				WithField("live", p.change.Live).
				Info("Config field set via API.")
		}
		out = append(out, p.change)
	}
	return out, nil
}

// setHypervisorsLive reconciles the configured hypervisor list with want,
// connecting to the added PKs and disconnecting the removed ones. Each
// Add/RemoveHypervisor persists the list itself.
func (v *Visor) setHypervisorsLive(want []cipher.PubKey) error {
	have := v.configuredHypervisors()
	in := func(set []cipher.PubKey, pk cipher.PubKey) bool {
		for _, p := range set {
			if p == pk {
				return true
			}
		}
		return false
	}
	for _, pk := range want {
		if !in(have, pk) {
			if err := v.AddHypervisor(pk); err != nil {
				return err
			}
		}
	}
	for _, pk := range have {
		if !in(want, pk) {
			if err := v.RemoveHypervisor(pk); err != nil {
				return err
			}
		}
	}
	return nil
}

// configTarget is a resolved config path: the addressed value, how to read and
// write it, the registry template used for the live lookup, and the bracket
// index the caller supplied (an app name, a knob name), if any.
type configTarget struct {
	typ  reflect.Type
	get  func() reflect.Value
	set  func(reflect.Value)
	tmpl string
	idx  string
}

// configSeg is one "name" or "name[index]" element of a dotted config path.
type configSeg struct {
	name   string
	idx    string
	hasIdx bool
}

// parseConfigPath splits a dotted path into segments, pulling out any
// bracketed index: "launcher.apps[skysocks].auto_start" becomes
// launcher / apps[skysocks] / auto_start.
func parseConfigPath(path string) ([]configSeg, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("empty config path")
	}
	parts := strings.Split(path, ".")
	segs := make([]configSeg, 0, len(parts))
	for _, part := range parts {
		if part == "" {
			return nil, fmt.Errorf("malformed config path %q: empty path element", path)
		}
		seg := configSeg{name: part}
		if open := strings.IndexByte(part, '['); open >= 0 {
			if !strings.HasSuffix(part, "]") {
				return nil, fmt.Errorf("malformed config path %q: unterminated '[' in %q", path, part)
			}
			seg.name = part[:open]
			seg.idx = part[open+1 : len(part)-1]
			seg.hasIdx = true
			if seg.name == "" || seg.idx == "" {
				return nil, fmt.Errorf("malformed config path %q: empty name or index in %q", path, part)
			}
		}
		segs = append(segs, seg)
	}
	return segs, nil
}

// secretConfigSeg reports whether a path element names visor identity or any
// other secret. Those are never editable through this surface — the same rule
// SetRuntimeConfig enforces on the whole-config write.
func secretConfigSeg(name string) bool {
	switch name {
	case "sk", "pk":
		return true
	}
	return strings.HasSuffix(name, "_sk") || strings.Contains(name, "secret_key")
}

// resolveConfigTarget walks a dotted path from the V1 struct value and returns
// the addressed field. root must be addressable (reflect.ValueOf(conf).Elem()).
func resolveConfigTarget(root reflect.Value, path string) (*configTarget, error) {
	segs, err := parseConfigPath(path)
	if err != nil {
		return nil, err
	}
	tmpl := make([]string, 0, len(segs))
	idx := ""
	cur := root
	for i, seg := range segs {
		if secretConfigSeg(seg.name) {
			return nil, fmt.Errorf("%s is not editable: visor identity and secret keys are refused", path)
		}
		block, err := derefConfigValue(cur)
		if err != nil {
			return nil, fmt.Errorf("%s: %s is not set; set the whole block first", path, strings.Join(tmpl, "."))
		}
		if block.Kind() != reflect.Struct {
			return nil, fmt.Errorf("%s: %q is not a config block", path, strings.Join(tmpl, "."))
		}
		field, ok := fieldByJSONTag(block, seg.name)
		if !ok {
			// A field the block MARSHALS but tags `json:"-"` is a mirror of
			// state that lives somewhere else (dmsg.sessions_count and its
			// siblings mirror Dmsg.Deployments, and are documented read-only
			// after unmarshal). It shows up in `config show`, so "no field"
			// reads as a lie. Say what it actually is.
			if mirroredJSONField(block, seg.name) {
				return nil, fmt.Errorf("%s is a read-only mirror of state held elsewhere in the config and cannot be set by path", path)
			}
			return nil, fmt.Errorf("unknown config path %q (no field %q)", path, seg.name)
		}
		cur = field
		if !seg.hasIdx {
			tmpl = append(tmpl, seg.name)
			continue
		}
		tmpl = append(tmpl, seg.name+"[*]")
		idx = seg.idx
		container, err := derefConfigValue(cur)
		if err != nil {
			return nil, fmt.Errorf("%s: %q is not set", path, seg.name)
		}
		switch container.Kind() {
		case reflect.Slice, reflect.Array:
			j := indexByNameField(container, seg.idx)
			if j < 0 {
				return nil, fmt.Errorf("%s: no entry named %q in %s", path, seg.idx, seg.name)
			}
			cur = container.Index(j)
		case reflect.Map:
			if container.Type().Key().Kind() != reflect.String {
				return nil, fmt.Errorf("%s: %s is not indexable by name", path, seg.name)
			}
			if i != len(segs)-1 {
				return nil, fmt.Errorf("%s: a map index must be the last path element", path)
			}
			return mapConfigTarget(container, seg.idx, strings.Join(tmpl, "."), idx), nil
		default:
			return nil, fmt.Errorf("%s: %s is not indexable", path, seg.name)
		}
	}
	if !cur.CanSet() {
		return nil, fmt.Errorf("%s is not settable", path)
	}
	target := cur
	return &configTarget{
		typ:  target.Type(),
		get:  func() reflect.Value { return target },
		set:  func(nv reflect.Value) { target.Set(nv) },
		tmpl: strings.Join(tmpl, "."),
		idx:  idx,
	}, nil
}

// mapConfigTarget builds a target for one key of a string-keyed map. Map values
// are not addressable, so read/write go through MapIndex / SetMapIndex; a nil
// map is allocated on first write.
func mapConfigTarget(m reflect.Value, key, tmpl, idx string) *configTarget {
	elemType := m.Type().Elem()
	kv := reflect.ValueOf(key).Convert(m.Type().Key())
	return &configTarget{
		typ: elemType,
		get: func() reflect.Value {
			got := m.MapIndex(kv)
			if !got.IsValid() {
				return reflect.Value{}
			}
			return got
		},
		set: func(nv reflect.Value) {
			if m.IsNil() {
				m.Set(reflect.MakeMap(m.Type()))
			}
			m.SetMapIndex(kv, nv)
		},
		tmpl: tmpl,
		idx:  idx,
	}
}

// derefConfigValue follows pointers/interfaces to the underlying value,
// erroring on a nil one rather than allocating a half-built config block.
func derefConfigValue(v reflect.Value) (reflect.Value, error) {
	for v.Kind() == reflect.Ptr || v.Kind() == reflect.Interface {
		if v.IsNil() {
			return reflect.Value{}, errors.New("nil config block")
		}
		v = v.Elem()
	}
	if !v.IsValid() {
		return reflect.Value{}, errors.New("invalid config value")
	}
	return v, nil
}

// fieldByJSONTag finds the struct field whose json tag name is name,
// descending into embedded (anonymous) structs the way encoding/json does.
func fieldByJSONTag(s reflect.Value, name string) (reflect.Value, bool) {
	t := s.Type()
	var embedded []int
	for i := 0; i < t.NumField(); i++ {
		sf := t.Field(i)
		if sf.PkgPath != "" { // unexported (V1.mu, Common.path, …)
			continue
		}
		tag := strings.Split(sf.Tag.Get("json"), ",")[0]
		if tag == "-" {
			continue
		}
		if tag == "" {
			tag = sf.Name
		}
		if sf.Anonymous && sf.Tag.Get("json") == "" {
			embedded = append(embedded, i)
			continue
		}
		if tag == name {
			return s.Field(i), true
		}
	}
	for _, i := range embedded {
		inner, err := derefConfigValue(s.Field(i))
		if err != nil || inner.Kind() != reflect.Struct {
			continue
		}
		if f, ok := fieldByJSONTag(inner, name); ok {
			return f, true
		}
	}
	return reflect.Value{}, false
}

// indexByNameField locates the element of a slice of structs whose "name" json
// field equals want — how launcher.apps[<app-name>] addresses an app entry
// without the operator having to count array positions.
func indexByNameField(list reflect.Value, want string) int {
	for i := 0; i < list.Len(); i++ {
		elem, err := derefConfigValue(list.Index(i))
		if err != nil || elem.Kind() != reflect.Struct {
			continue
		}
		f, ok := fieldByJSONTag(elem, "name")
		if !ok || f.Kind() != reflect.String {
			continue
		}
		if f.String() == want {
			return i
		}
	}
	return -1
}

// decodeConfigValue turns the caller's raw JSON into a value of the field's
// type, which is the type check: a string into an int field, an unknown key in
// a nested object, or a malformed public key all fail here.
//
// The CLI cannot know the field's type when it quotes a bare word, so two
// forgiving retries follow a failed strict decode: a bare scalar into a string
// field ("info" for log_level), and a quoted scalar into a non-string field
// ("8080" for a port).
func decodeConfigValue(t reflect.Type, raw json.RawMessage) (reflect.Value, error) {
	nv := reflect.New(t)
	err := strictDecode(raw, nv.Interface())
	if err == nil {
		return nv.Elem(), nil
	}
	var unquoted string
	if uErr := json.Unmarshal(raw, &unquoted); uErr == nil {
		retry := reflect.New(t)
		if strictDecode(json.RawMessage(unquoted), retry.Interface()) == nil {
			return retry.Elem(), nil
		}
	} else {
		quoted, mErr := json.Marshal(string(raw))
		if mErr == nil {
			retry := reflect.New(t)
			if strictDecode(quoted, retry.Interface()) == nil {
				return retry.Elem(), nil
			}
		}
	}
	return reflect.Value{}, fmt.Errorf("value %s is not valid for this field (%s): %w", string(raw), t, err)
}

func strictDecode(raw json.RawMessage, into interface{}) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(into); err != nil {
		return err
	}
	return nil
}

// marshalConfigValue renders a field's current value, answering null for a map
// key that is not present.
func marshalConfigValue(v reflect.Value) (json.RawMessage, error) {
	if !v.IsValid() {
		return json.RawMessage("null"), nil
	}
	return json.Marshal(v.Interface())
}

// setEmbeddedProxyEnabledPersist / setEmbeddedProxyUpstreamPersist apply a
// resolving-proxy field to the RUNNING resolver and then write it down.
//
// The runtime setters in api_proxies.go are split on persistence:
// SetEmbeddedProxyBind documents that it persists, while SetEmbeddedProxyEnabled
// and SetEmbeddedProxyUpstream only touch the running resolver — `proxies set`
// is a runtime command, and that is a defensible thing for it to be. `config
// set` is not: its whole contract is that the value is now the config. So the
// flush lives here rather than changing what `proxies set` means.
func (v *Visor) setEmbeddedProxyEnabledPersist(kind string, enable bool) error {
	if err := v.SetEmbeddedProxyEnabled(kind, enable); err != nil {
		return err
	}
	switch kind {
	case "dmsg":
		if v.conf.DmsgWeb == nil {
			v.conf.DmsgWeb = &visorconfig.DmsgWebConfig{}
		}
		v.conf.DmsgWeb.Enable = enable
	case "skynet":
		if v.conf.SkynetWeb == nil {
			v.conf.SkynetWeb = &visorconfig.SkynetWebConfig{}
		}
		v.conf.SkynetWeb.Enable = enable
	default:
		return fmt.Errorf("unknown proxy kind %q", kind)
	}
	return v.conf.Flush()
}

func (v *Visor) setEmbeddedProxyUpstreamPersist(kind, addr string) error {
	if err := v.SetEmbeddedProxyUpstream(kind, addr); err != nil {
		return err
	}
	switch kind {
	case "dmsg":
		if v.conf.DmsgWeb == nil {
			v.conf.DmsgWeb = &visorconfig.DmsgWebConfig{}
		}
		v.conf.DmsgWeb.UpstreamSOCKS = addr
	case "skynet":
		if v.conf.SkynetWeb == nil {
			v.conf.SkynetWeb = &visorconfig.SkynetWebConfig{}
		}
		v.conf.SkynetWeb.UpstreamSOCKS = addr
	default:
		return fmt.Errorf("unknown proxy kind %q", kind)
	}
	return v.conf.Flush()
}

// mirroredJSONField reports whether the block has a field NAMED for this JSON
// key but tagged `json:"-"` — a mirror the block's own MarshalJSON writes out
// while the canonical value lives elsewhere. Matched on the Go field name in
// UpperCamel, which is how every such mirror in the config is spelled.
func mirroredJSONField(block reflect.Value, name string) bool {
	want := strings.ReplaceAll(strings.Title(strings.ReplaceAll(name, "_", " ")), " ", "") //nolint:staticcheck // ASCII config keys
	t := block.Type()
	for i := 0; i < t.NumField(); i++ {
		sf := t.Field(i)
		if sf.PkgPath != "" {
			continue
		}
		if strings.Split(sf.Tag.Get("json"), ",")[0] != "-" {
			continue
		}
		if strings.EqualFold(sf.Name, want) {
			return true
		}
	}
	return false
}
