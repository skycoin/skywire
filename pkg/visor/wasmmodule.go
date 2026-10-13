// Package visor pkg/visor/wasmmodule.go c3-vis-core
package visor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"

	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
	"github.com/skycoin/skywire/pkg/wasmhv/execwasm"
)

// The js/wasm module a visor serves for the desk is not packaged. It is
// fetched and refreshed only when the desk is opened: the visor asks its
// source visor over dmsg for the module's manifest, and when the sha256
// differs it downloads the module, checks it and swaps it in. With no module
// yet and no source to reach, it installs the latest release's module over
// HTTPS. Every visor serves its own copy under wasmModuleRoute on its dmsg log
// server.
const (
	wasmModuleRoute           = "/wasm/"
	wasmModuleCheckEvery      = 5 * time.Minute
	wasmModuleManifestTimeout = 15 * time.Second
	wasmModuleFetchTimeout    = 15 * time.Minute
)

// wasmModuleReleases is where a module is fetched from when there is none and
// no source to reach, checked against the release's manifest. That never
// replaces a module, which can be newer than any release.
var wasmModuleReleases = "https://github.com/skycoin/skywire/releases/"

// releaseTag is the version of a binary built from a release tag.
var releaseTag = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+$`)

// wasmModuleRelease is the release a binary of version takes its module from:
// its own, or the latest for a build between releases.
func wasmModuleRelease(version string) string {
	if releaseTag.MatchString(version) {
		return wasmModuleReleases + "download/" + version + "/"
	}
	return wasmModuleReleases + "latest/download/"
}

// wasmModuleRefresher keeps one on-disk module current with its source.
type wasmModuleRefresher struct {
	path   string
	source cipher.PubKey // null for none
	client *http.Client
	// up reports whether the visor can reach the source yet.
	up func() bool
	// release is the base URL a missing module is fetched from, "" for none.
	release string
	web     *http.Client
	log     *logging.Logger

	mu        sync.Mutex
	lastCheck time.Time
	running   chan struct{} // closed when the refresh in progress ends
}

// wasmModule is the refresher for the module this visor serves, nil for an
// explicit path, which is a developer's own build. The source visor is where
// everyone else's copy comes from, and the source itself refreshes from none.
func (v *Visor) wasmModule() *wasmModuleRefresher {
	v.wasmModuleOnce.Do(func() {
		hc := v.conf.Hypervisor
		if hc != nil && hc.WasmServe != nil && hc.WasmServe.ExecWasm != "" {
			return
		}
		m := newReleaseModule(execwasm.DefaultPath(), v.log)
		if m == nil {
			return
		}
		src, err := wasmModuleSource(hc)
		if err != nil {
			v.log.WithError(err).Warn("js/wasm module source is not a public key; the module will not be refreshed")
		} else if !src.Null() && src != v.conf.PK {
			m.source = src
			m.client = &http.Client{Transport: v.dmsgHTTPTransport()}
			m.up = func() bool { return v.dmsgC != nil }
		}
		v.wasmModuleRef = m
	})
	return v.wasmModuleRef
}

// newReleaseModule is a refresher that only installs a missing module at path
// from the latest release, nil when there is no path.
func newReleaseModule(path string, log *logging.Logger) *wasmModuleRefresher {
	if path == "" {
		return nil
	}
	return &wasmModuleRefresher{path: path, release: wasmModuleRelease(buildinfo.Version()), web: &http.Client{}, log: log}
}

// missing reports whether there is no module at the path yet.
func (m *wasmModuleRefresher) missing() bool {
	_, err := os.Stat(m.path)
	return err != nil
}

// sourceUp reports whether there is a source visor and it can be reached.
func (m *wasmModuleRefresher) sourceUp() bool {
	return !m.source.Null() && (m.up == nil || m.up())
}

// wasmModuleSource is the visor the module is refreshed from, null when the
// deployment names none.
func wasmModuleSource(hc *visorconfig.HypervisorConfig) (cipher.PubKey, error) {
	s := deployment.Prod.WasmModuleSource
	if hc != nil && hc.WasmModuleSource != "" {
		s = hc.WasmModuleSource
	}
	var pk cipher.PubKey
	if s == "" {
		return pk, nil
	}
	err := pk.Set(s)
	return pk, err
}

// refresh brings the module up to its source's when it was last checked more
// than wasmModuleCheckEvery ago, and waits for a refresh already under way.
// The download outlives ctx, so a desk that gives up does not waste it.
func (m *wasmModuleRefresher) refresh(ctx context.Context) {
	if m == nil {
		return
	}
	m.mu.Lock()
	if ch := m.running; ch != nil {
		m.mu.Unlock()
		wait(ctx, ch)
		return
	}
	// A module on disk is only refreshed from a source that can be reached; a
	// missing one can still come from the release.
	idle := !m.sourceUp() && (!m.missing() || m.release == "")
	if time.Since(m.lastCheck) < wasmModuleCheckEvery || idle {
		m.mu.Unlock()
		return
	}
	m.lastCheck = time.Now()
	ch := make(chan struct{})
	m.running = ch
	m.mu.Unlock()

	go func() {
		defer func() {
			m.mu.Lock()
			m.running = nil
			m.mu.Unlock()
			close(ch)
		}()
		uctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), wasmModuleFetchTimeout)
		defer cancel()
		if err := m.update(uctx); err != nil {
			m.log.WithError(err).Warn("Could not fetch the js/wasm module")
		}
	}()
	wait(ctx, ch)
}

// await waits for a refresh already under way, without starting one.
func (m *wasmModuleRefresher) await(ctx context.Context) {
	if m == nil {
		return
	}
	m.mu.Lock()
	ch := m.running
	m.mu.Unlock()
	if ch != nil {
		wait(ctx, ch)
	}
}

func wait(ctx context.Context, ch <-chan struct{}) {
	select {
	case <-ch:
	case <-ctx.Done():
	}
}

func (m *wasmModuleRefresher) url(name string) string {
	return fmt.Sprintf("http://%s:%d%s%s", m.source.Hex(), visorconfig.DmsgHTTPPort, wasmModuleRoute, name)
}

// moduleOrigin is somewhere a module and its manifest can be downloaded from.
type moduleOrigin struct {
	name   string
	url    func(name string) string
	client *http.Client
}

func (m *wasmModuleRefresher) fromSource() moduleOrigin {
	return moduleOrigin{name: "source visor " + m.source.Hex(), url: m.url, client: m.client}
}

func (m *wasmModuleRefresher) fromRelease() moduleOrigin {
	return moduleOrigin{name: m.release, url: func(name string) string { return m.release + name }, client: m.web}
}

// update replaces the module when the source's manifest names another one,
// and installs the release's when there is none and the source cannot give it.
func (m *wasmModuleRefresher) update(ctx context.Context) error {
	if m.sourceUp() {
		err := m.fetch(ctx, m.fromSource())
		if err == nil || !m.missing() || m.release == "" {
			return err
		}
		m.log.WithError(err).Warn("Could not fetch the js/wasm module from its source visor; trying the release")
	}
	if m.missing() && m.release != "" {
		return m.fetch(ctx, m.fromRelease())
	}
	return nil
}

// fetch installs the module from o when its manifest names another one.
func (m *wasmModuleRefresher) fetch(ctx context.Context, o moduleOrigin) error {
	want, err := m.manifest(ctx, o)
	if err != nil {
		return err
	}
	if have, err := execwasm.ReadManifest(m.path); err == nil && have.SHA256 == want.SHA256 && !m.missing() {
		return nil
	}
	m.log.WithField("version", want.Version).WithField("from", o.name).Info("Fetching the js/wasm module")

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, o.url(execwasm.ModuleName), nil)
	if err != nil {
		return err
	}
	resp, err := o.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("module: %s", resp.Status)
	}

	dir := filepath.Dir(m.path)
	tmp, err := os.CreateTemp(dir, ".skywire.wasm.gz-")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) //nolint:errcheck
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, h), io.LimitReader(resp.Body, want.Size+1))
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if n != want.Size || hex.EncodeToString(h.Sum(nil)) != want.SHA256 {
		return fmt.Errorf("module does not match its manifest (%d bytes, want %d)", n, want.Size)
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil { //nolint:gosec // served to anyone who asks
		return err
	}
	mf, err := json.Marshal(want)
	if err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), m.path); err != nil {
		return err
	}
	if err := os.WriteFile(execwasm.ManifestPath(m.path), append(mf, '\n'), 0o644); err != nil { //nolint:gosec // served to anyone who asks
		return err
	}
	m.log.WithField("version", want.Version).Info("Installed the js/wasm module")
	return nil
}

// manifest asks o which module is current.
func (m *wasmModuleRefresher) manifest(ctx context.Context, o moduleOrigin) (execwasm.Manifest, error) {
	var want execwasm.Manifest
	ctx, cancel := context.WithTimeout(ctx, wasmModuleManifestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, o.url(execwasm.ManifestName), nil)
	if err != nil {
		return want, err
	}
	resp, err := o.client.Do(req)
	if err != nil {
		return want, err
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		return want, fmt.Errorf("manifest: %s", resp.Status)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<10)).Decode(&want); err != nil {
		return want, fmt.Errorf("manifest: %w", err)
	}
	if len(want.SHA256) != 64 || want.Size <= 0 {
		return want, errors.New("manifest names no module")
	}
	return want, nil
}

// ready waits for the module a request is about to be served: it fetches one
// when there is none yet, and otherwise waits for a refresh under way.
func (m *wasmModuleRefresher) ready(ctx context.Context) {
	if m != nil && m.missing() {
		m.refresh(ctx)
		return
	}
	m.await(ctx)
}
