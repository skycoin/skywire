// Package visor pkg/visor/api_config_mirror.go c3-vis-core
package visor

import (
	"os"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywireconfig/autoconfigcmd"
	"github.com/skycoin/skywire/pkg/skywireconfig/skyenvfile"
)

// On a package install, and in the browser visor, skywire.json is rebuilt by
// `skywire autoconfig` from /etc/skywire.conf on every update (every page load
// in the browser). A field set on the running visor survives that only if the
// variable autoconfig reads is changed too. autoconfigFlagFor names, per config
// path, the autoconfig flag whose variable holds it and the flag text for a
// value.
var autoconfigFlagFor = map[string]func(nv reflect.Value) (flag, value string){
	"is_public":                 boolFlag("public"),
	"log_level":                 stringFlag("loglvl"),
	"hypervisors":               pkListFlag("hvpks"),
	"pty.whitelist":             pkListFlag("dmsgpty-pks"),
	"routing.min_hops":          uintFlag("min-hops"),
	"routing.calculate_routes":  boolFlag("calculate-routes"),
	"routing.mux_fec":           boolFlag("mux-fec"),
	"flight_recorder":           boolFlag("flight-recorder"),
	"hypervisor.ui_disable":     boolFlag("hv-ui-disable"),
	"reward_address":            stringFlag("rewardaddr"),
	"hypervisor.enable":         boolFlag("ishv"),
	"hypervisor.enable_auth":    boolFlag("hv-auth"),
	"dmsg_web.enable":           boolFlag("dmsgweb"),
	"dmsg_web.proxy_addr":       stringFlag("dmsgweb-addr"),
	"dmsg_web.upstream_socks":   stringFlag("dmsgweb-upstream"),
	"skynet_web.enable":         boolFlag("skynetweb"),
	"skynet_web.proxy_addr":     stringFlag("skynetweb-addr"),
	"skynet_web.upstream_socks": stringFlag("skynetweb-upstream"),
	"transport.public_autoconnect": func(nv reflect.Value) (string, string) {
		return "disable-public-autoconn", strconv.FormatBool(!nv.Bool())
	},
}

func boolFlag(flag string) func(reflect.Value) (string, string) {
	return func(nv reflect.Value) (string, string) { return flag, strconv.FormatBool(nv.Bool()) }
}

func stringFlag(flag string) func(reflect.Value) (string, string) {
	return func(nv reflect.Value) (string, string) { return flag, nv.String() }
}

func uintFlag(flag string) func(reflect.Value) (string, string) {
	return func(nv reflect.Value) (string, string) { return flag, strconv.FormatUint(nv.Uint(), 10) }
}

func pkListFlag(flag string) func(reflect.Value) (string, string) {
	return func(nv reflect.Value) (string, string) {
		pks, _ := nv.Interface().([]cipher.PubKey) //nolint:errcheck
		return flag, joinPKs(pks)
	}
}

func joinPKs(pks []cipher.PubKey) string {
	s := make([]string, 0, len(pks))
	for _, pk := range pks {
		s = append(s, pk.String())
	}
	return strings.Join(s, ",")
}

// skyenvManaged reports the skywire.conf this visor's config was generated
// from, or "" when it was not generated from one.
func (v *Visor) skyenvManaged() string {
	if v.conf == nil || v.conf.Skyenv == "" {
		return ""
	}
	if _, err := os.Stat(v.conf.Skyenv); err != nil {
		v.log.WithError(err).WithField("path", v.conf.Skyenv).
			Warn("the skywire.conf this config was generated from is gone; live changes are not saved to it")
		return ""
	}
	return v.conf.Skyenv
}

// mirrorToSkyenv writes the given flag values into skywire.conf when autoconfig
// manages this visor's config. Best effort: a failure is logged, since the
// change already took effect and only the next regeneration would lose it.
func (v *Visor) mirrorToSkyenv(flags [][2]string) {
	path := v.skyenvManaged()
	if path == "" || len(flags) == 0 {
		return
	}
	edits := make([]skyenvfile.Edit, 0, len(flags))
	for _, f := range flags {
		e, err := autoconfigcmd.EditFor(f[0], f[1])
		if err != nil {
			v.log.WithError(err).Warn("no skywire.conf variable for a config field")
			continue
		}
		edits = append(edits, e)
	}
	if err := skyenvfile.Update(path, edits); err != nil {
		names := make([]string, 0, len(edits))
		for _, e := range edits {
			names = append(names, e.Key)
		}
		v.log.WithError(err).WithField("path", path).WithField("settings", names).
			Warn("could not save live changes to skywire.conf; the next autoconfig run will undo them")
	}
}

// watchSkyenv records the mirrored settings as generated, then after each
// config write saves the ones that changed to skywire.conf.
func (v *Visor) watchSkyenv() {
	if v.conf == nil {
		return
	}
	v.skyenvSeen = v.mirroredValues()
	v.conf.SetAfterFlush(v.mirrorChanges)
}

// mirrorChanges saves every mirrored setting that changed since the last save.
func (v *Visor) mirrorChanges() {
	v.skyenvMu.Lock()
	defer v.skyenvMu.Unlock()
	now := v.mirroredValues()
	var flags [][2]string
	for path, fv := range now {
		if v.skyenvSeen[path] != fv {
			flags = append(flags, fv)
		}
	}
	v.skyenvSeen = now
	sort.Slice(flags, func(i, j int) bool { return flags[i][0] < flags[j][0] })
	v.mirrorToSkyenv(flags)
}

// mirroredValues is the autoconfig flag and value of every mirrored setting.
// A setting whose block is absent counts as its zero value, so removing the
// block is saved too.
func (v *Visor) mirroredValues() map[string][2]string {
	root := reflect.ValueOf(v.conf).Elem()
	out := make(map[string][2]string, len(autoconfigFlagFor))
	for path, flagFor := range autoconfigFlagFor {
		var nv reflect.Value
		if tgt, err := resolveConfigTarget(root, path); err == nil {
			nv = tgt.get()
		} else if t := configFieldType(root.Type(), path); t != nil {
			nv = reflect.Zero(t)
		} else {
			continue
		}
		flag, val := flagFor(nv)
		out[path] = [2]string{flag, val}
	}
	return out
}

// configFieldType walks a dotted path of json names through struct types.
func configFieldType(t reflect.Type, path string) reflect.Type {
	for _, name := range strings.Split(path, ".") {
		for t.Kind() == reflect.Pointer {
			t = t.Elem()
		}
		if t.Kind() != reflect.Struct {
			return nil
		}
		f, ok := structFieldByJSONName(t, name)
		if !ok {
			return nil
		}
		t = f.Type
	}
	return t
}

func structFieldByJSONName(t reflect.Type, name string) (reflect.StructField, bool) {
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.Anonymous && f.Tag.Get("json") == "" {
			et := f.Type
			if et.Kind() == reflect.Pointer {
				et = et.Elem()
			}
			if et.Kind() != reflect.Struct {
				continue
			}
			if sf, ok := structFieldByJSONName(et, name); ok {
				return sf, true
			}
			continue
		}
		if tag, _, _ := strings.Cut(f.Tag.Get("json"), ","); tag == name {
			return f, true
		}
	}
	return reflect.StructField{}, false
}
