// Package clilog cmd/skywire-cli/commands/log/dmsghttp_client.go
//
// Shared dmsg-bootstrap + http-client helper for the per-visor
// survey-whitelist endpoints. Pre-fix, every subcommand that
// reached out over dmsghttp had to re-implement the SK resolution,
// dmsg bootstrap, and transport-pool wiring inline. This file
// centralizes that boilerplate so `cli log info|file|pprof|stats|
// uptime|reward` share one resolution policy + one dmsg client.
//
// Endpoints reached this way are all gated by the remote visor's
// survey_whitelist (see pkg/visor/logserver/api.go:131-208 — the
// `authRoute` group). The CLI's local PK is what gets allowlisted;
// no per-request signing, so SK loading just selects which PK we
// authenticate AS.
package clilog

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cmdutil"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/dmsg/dmsghttp"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

// resolveSurveySK selects the SK used as the CLI's identity for
// survey-whitelist-gated requests. Resolution order (first hit wins):
//
//  1. --sk flag (the existing `sk` cobra var, when non-zero)
//  2. DMSGCURL_SK env (same env the bulk-collection path honors)
//  3. survey_client_sk from /opt/skywire/skywire.json (if present)
//  4. fresh ephemeral keypair — useful for one-off calls against
//     a wide-open whitelist or for self-targeted requests where the
//     remote's whitelist includes ephemeral access.
//
// Returns the selected SK and a label naming the source — the label
// is logged at debug so an operator can verify which precedence
// layer fired without guessing.
func resolveSurveySK() (cipher.SecKey, string) {
	var zero cipher.SecKey
	if sk != zero {
		return sk, "--sk flag"
	}
	if env := os.Getenv("DMSGCURL_SK"); env != "" {
		var k cipher.SecKey
		if err := k.Set(env); err == nil {
			return k, "DMSGCURL_SK env"
		}
	}
	// /opt/skywire/skywire.json is the standard install location; the
	// same file the visor reads. Reading FAILURES are silent here —
	// missing file or missing field both fall through to ephemeral.
	if conf, err := visorconfig.ReadFile(visorconfig.SkywireConfig()); err == nil && conf != nil && conf.SurveyClientSK != "" {
		var k cipher.SecKey
		if err := k.Set(conf.SurveyClientSK); err == nil {
			return k, "skywire.json survey_client_sk"
		}
	}
	_, fresh := cipher.GenerateKeyPair()
	return fresh, "ephemeral"
}

// dmsgHTTPClient bootstraps a dmsg client off the resolved SK and
// returns a pooled http.Client whose transport rides dmsg streams.
// The returned cleanup func tears down the dmsg client; callers MUST
// defer it.
//
// timeout caps per-request work; pprof CPU profiles can be 30s+ so
// callers needing slow endpoints pass a longer value (or zero for
// unbounded — http.Client uses no per-request timeout in that case,
// only ctx cancellation).
func dmsgHTTPClient(ctx context.Context, timeout time.Duration) (*http.Client, func(), error) {
	log := logging.MustGetLogger("log-cli")
	resolvedSK, source := resolveSurveySK()
	pk, err := resolvedSK.PubKey()
	if err != nil {
		// SecKey.Set already validated the SK shape; PubKey derivation
		// is deterministic. Treat as ephemeral fallback rather than
		// fatal — the caller's request will then fail with the
		// remote's whitelist signal, which is more informative than
		// dying here.
		pk, resolvedSK = cipher.GenerateKeyPair()
		source = "ephemeral (PK derivation failed)"
	}
	log.WithField("source", source).WithField("local_pk", pk.String()).Debug("Resolved survey-whitelist identity for CLI request")

	bootstrap, err := cmdutil.BootstrapDmsg(ctx, log, pk, resolvedSK, dmsg.Prod.DmsgServers, dmsgDiscURL, "")
	if err != nil {
		return nil, func() {}, fmt.Errorf("dmsg bootstrap: %w", err)
	}

	hc := &http.Client{
		Transport: dmsghttp.MakeHTTPTransport(ctx, bootstrap.Client),
		Timeout:   timeout,
	}
	cleanup := func() {
		hc.CloseIdleConnections()
		bootstrap.Close()
	}
	return hc, cleanup, nil
}

// fetchSurveyEndpoint GETs dmsg://<pk>:80/<path> and writes the body
// to w. Single helper used by every subcommand that pulls a binary
// or text response. 200 OK is required; non-200 surfaces as an error
// with the status code so callers can distinguish whitelist denial
// (401) from missing-file (404) from server bug (500).
//
// Streams (io.Copy) rather than buffering — pprof profiles + visor
// logs can be megabytes.
func fetchSurveyEndpoint(ctx context.Context, hc *http.Client, pk cipher.PubKey, path string, w io.Writer) error {
	target := fmt.Sprintf("dmsg://%s:80%s", pk, path)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	resp, err := hc.Do(req) //nolint:gosec
	if err != nil {
		return fmt.Errorf("dmsghttp dial: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("dmsg://%s%s returned status %d (401 = not in survey_whitelist; 404 = endpoint missing on this visor)", pk, path, resp.StatusCode)
	}
	if _, err := io.Copy(w, resp.Body); err != nil {
		return fmt.Errorf("copy body: %w", err)
	}
	return nil
}

// fetchSurveyJSON is a typed sibling of fetchSurveyEndpoint for
// endpoints that return JSON. Decodes into v; v should be a pointer
// to a struct or map. Same error surface as the byte-stream helper.
func fetchSurveyJSON(ctx context.Context, hc *http.Client, pk cipher.PubKey, path string, v any) error {
	target := fmt.Sprintf("dmsg://%s:80%s", pk, path)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	resp, err := hc.Do(req) //nolint:gosec
	if err != nil {
		return fmt.Errorf("dmsghttp dial: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("dmsg://%s%s returned status %d", pk, path, resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		return fmt.Errorf("decode JSON: %w", err)
	}
	return nil
}

// parseTargetPK pulls the first positional arg as a public key, with
// a uniform error message across subcommands. Centralized so the
// hint text on bad input stays consistent.
func parseTargetPK(arg string) (cipher.PubKey, error) {
	var pk cipher.PubKey
	if err := pk.Set(arg); err != nil {
		return cipher.PubKey{}, fmt.Errorf("invalid visor PK %q: %w", arg, err)
	}
	return pk, nil
}

// _ keeps deployment imported even if a future trim of the helper
// happens to drop the only deployment.Prod reference; the bootstrap
// path needs it.
var _ = deployment.Prod
