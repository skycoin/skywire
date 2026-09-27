// Package visor pkg/visor/api_skyenv.go c3-vis-core
//
// Reading and editing /etc/skywire.conf from the running visor.
//
// skywire.json is a derived file: `skywire autoconfig` regenerates it from
// skywire.conf on every package install and update, and the browser visor
// does so on every page load. A setting that lives only in the json — which
// is where SetConfigFields writes — lasts until that next regen. The .conf
// file is the layer that survives, and until now the only way to edit it was
// `skywire autoconfig --<flag>` in a shell on the host.
//
// Edits are addressed by autoconfig flag name and go through the same
// flag→variable table (autoconfigcmd) and the same writer (skyenvfile.Update)
// as that command, so a setting changed here is byte-for-byte the setting
// `skywire autoconfig --<flag>` would have written.
package visor

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/skycoin/skywire/pkg/skywireconfig/autoconfigcmd"
	"github.com/skycoin/skywire/pkg/skywireconfig/skyenvfile"
	"github.com/skycoin/skywire/pkg/visor/visorapi"
)

// skyenvMu serializes SetSkyenv: Update is a read-modify-rename, and two
// interleaved calls would each drop the other's edit.
var skyenvMu sync.Mutex

// skyenvRefused are flags a remote settings edit may not make. The secret
// key IS the visor's identity; replacing it belongs at the host, with
// `skywire autoconfig --sk`, not behind a form field.
var skyenvRefused = map[string]string{
	"sk": "the secret key is the visor's identity; change it on the host with `skywire autoconfig --sk`",
}

// Skyenv implements visorapi.API.
func (v *Visor) Skyenv() (visorapi.SkyenvState, error) {
	path := skyenvfile.Path()
	st := visorapi.SkyenvState{Path: path, Values: map[string]string{}, Flags: autoconfigcmd.Describe()}
	vals, err := skyenvfile.Values(path)
	switch {
	case err == nil:
		st.Exists = true
		for k := range vals {
			if visorapi.SkyenvSecret(k) && vals[k] != "" {
				vals[k] = visorapi.SkyenvRedacted
			}
		}
		st.Values = vals
		st.Writable = skyenvWritable(path)
	case errors.Is(err, os.ErrNotExist):
	default:
		return st, err
	}
	return st, nil
}

// SetSkyenv implements visorapi.API.
func (v *Visor) SetSkyenv(req visorapi.SkyenvEdits) (visorapi.SkyenvState, error) {
	var edits []skyenvfile.Edit
	for flag, val := range req.Set {
		if why, ok := skyenvRefused[flag]; ok {
			return visorapi.SkyenvState{}, fmt.Errorf("--%s: %s", flag, why)
		}
		e, err := autoconfigcmd.EditFor(flag, val)
		if err != nil {
			return visorapi.SkyenvState{}, err
		}
		if visorapi.SkyenvSecret(e.Key) && val == visorapi.SkyenvRedacted {
			continue // a form echoing back what Skyenv showed it: leave the secret alone
		}
		edits = append(edits, e)
	}
	for _, flag := range req.Unset {
		if why, ok := skyenvRefused[flag]; ok {
			return visorapi.SkyenvState{}, fmt.Errorf("--%s: %s", flag, why)
		}
		key, ok := autoconfigcmd.KeyFor(flag)
		if !ok {
			return visorapi.SkyenvState{}, fmt.Errorf("%q is not an autoconfig flag that writes skywire.conf", flag)
		}
		edits = append(edits, skyenvfile.Edit{Key: key, Unset: true})
	}

	skyenvMu.Lock()
	defer skyenvMu.Unlock()
	path := skyenvfile.Path()
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return visorapi.SkyenvState{}, fmt.Errorf("%s does not exist yet; `skywire autoconfig` creates it", path)
	}
	if err := skyenvfile.Update(path, edits); err != nil {
		return visorapi.SkyenvState{}, err
	}
	return v.Skyenv()
}

// skyenvWritable reports whether Update could replace path: it writes a temp
// file beside it and renames it over, so what matters is the directory.
func skyenvWritable(path string) bool {
	f, err := os.CreateTemp(filepath.Dir(path), ".skywire.conf.probe-")
	if err != nil {
		return false
	}
	name := f.Name()
	_ = f.Close()       //nolint:errcheck
	_ = os.Remove(name) //nolint:errcheck
	return true
}
