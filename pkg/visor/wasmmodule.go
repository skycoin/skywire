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
	"sync"
	"time"

	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
	"github.com/skycoin/skywire/pkg/wasmhv/execwasm"
)

// The js/wasm module a visor serves for the desk lives beside its binary. It
// is refreshed only when the desk is opened: the visor asks its source visor
// over dmsg for the module's manifest, and when the sha256 differs it
// downloads the module, checks it and swaps it in. Every visor serves its own
// copy under wasmModuleRoute on its dmsg log server.
const (
	wasmModuleRoute           = "/wasm/"
	wasmModuleCheckEvery      = 5 * time.Minute
	wasmModuleManifestTimeout = 15 * time.Second
	wasmModuleFetchTimeout    = 15 * time.Minute
)

// wasmModuleRefresher keeps one on-disk module current with its source.
type wasmModuleRefresher struct {
	path   string
	source cipher.PubKey
	client *http.Client
	// up reports whether the visor can reach the source yet.
	up  func() bool
	log *logging.Logger

	mu        sync.Mutex
	lastCheck time.Time
	running   chan struct{} // closed when the refresh in progress ends
}

// wasmModule is the refresher for the module this visor serves, nil when it
// serves none or does not refresh it: an explicit path is a developer's own
// build, and the source visor is where everyone else's copy comes from.
func (v *Visor) wasmModule() *wasmModuleRefresher {
	v.wasmModuleOnce.Do(func() {
		hc := v.conf.Hypervisor
		if hc != nil && hc.WasmServe != nil && hc.WasmServe.ExecWasm != "" {
			return
		}
		src, err := wasmModuleSource(hc)
		if err != nil {
			v.log.WithError(err).Warn("js/wasm module source is not a public key; the module will not be refreshed")
			return
		}
		if src.Null() || src == v.conf.PK {
			return
		}
		path := execwasm.DefaultPath()
		if path == "" {
			return
		}
		v.wasmModuleRef = &wasmModuleRefresher{
			path:   path,
			source: src,
			client: &http.Client{Transport: v.dmsgHTTPTransport()},
			up:     func() bool { return v.dmsgC != nil },
			log:    v.log,
		}
	})
	return v.wasmModuleRef
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
	if time.Since(m.lastCheck) < wasmModuleCheckEvery || (m.up != nil && !m.up()) {
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
			m.log.WithError(err).WithField("source", m.source.Hex()).Warn("Could not refresh the js/wasm module; serving the one on disk")
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

// update replaces the module when the source's manifest names another one.
func (m *wasmModuleRefresher) update(ctx context.Context) error {
	want, err := m.manifest(ctx)
	if err != nil {
		return err
	}
	if have, err := execwasm.ReadManifest(m.path); err == nil && have.SHA256 == want.SHA256 {
		return nil
	}
	m.log.WithField("version", want.Version).WithField("source", m.source.Hex()).Info("Fetching a newer js/wasm module")

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.url(execwasm.ModuleName), nil)
	if err != nil {
		return err
	}
	resp, err := m.client.Do(req)
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
	m.log.WithField("version", want.Version).Info("Installed a newer js/wasm module")
	return nil
}

// manifest asks the source which module is current.
func (m *wasmModuleRefresher) manifest(ctx context.Context) (execwasm.Manifest, error) {
	var want execwasm.Manifest
	ctx, cancel := context.WithTimeout(ctx, wasmModuleManifestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.url(execwasm.ManifestName), nil)
	if err != nil {
		return want, err
	}
	resp, err := m.client.Do(req)
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
