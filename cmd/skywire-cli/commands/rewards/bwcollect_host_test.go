// Package clirewards cmd/skywire-cli/commands/rewards/bwcollect_host_test.go c4-vis-cli
package clirewards

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cxo/storeconfig"
	"github.com/skycoin/skywire/pkg/deployment/tpd/api"
	tpdmetrics "github.com/skycoin/skywire/pkg/deployment/tpd/metrics"
	tpdstore "github.com/skycoin/skywire/pkg/deployment/tpd/store"
	"github.com/skycoin/skywire/pkg/httpauth"
	"github.com/skycoin/skywire/pkg/logging"
)

// TestHostKeysFindVisorsOnOneHost checks that bw-collect sees two visors as
// one host by the IP a dmsg server signed, not the IP a survey claims, and by
// a shared boot_id, and that visors with no MAC are not grouped by it.
func TestHostKeysFindVisorsOnOneHost(t *testing.T) {
	srvPK, srvSK := cipher.GenerateKeyPair()
	known := ipProofServerKnown
	ipProofServerKnown = func(pk cipher.PubKey) bool { return pk == srvPK }
	t.Cleanup(func() { ipProofServerKnown = known })

	day := time.Date(2026, time.December, 1, 0, 0, 0, 0, time.UTC)
	keys := func() (cipher.PubKey, cipher.SecKey) { return cipher.GenerateKeyPair() }
	ifc := func(mac string) []map[string]string { return []map[string]string{{"ifname": "eth0", "address": mac}} }

	surveys := t.TempDir()
	write := func(pk cipher.PubKey, survey map[string]any) string {
		b, err := json.Marshal(survey)
		require.NoError(t, err)
		require.NoError(t, os.MkdirAll(filepath.Join(surveys, pk.Hex()), 0o750))
		require.NoError(t, os.WriteFile(filepath.Join(surveys, pk.Hex(), "node-info.json"), b, 0o600))
		return pk.Hex()
	}
	signed := func(mac, claimedIP, bootID string) string {
		pk, sk := keys()
		return write(pk, map[string]any{"ip_address": claimedIP, "ip_proof": ipProofSign(t, pk, sk, srvPK, srvSK, day), "ip_addr": ifc(mac), "boot_id": bootID})
	}
	unsigned := func(mac, claimedIP, bootID string) string {
		pk, _ := keys()
		return write(pk, map[string]any{"ip_address": claimedIP, "ip_addr": ifc(mac), "boot_id": bootID})
	}

	// Each claims its own IP, but the dmsg server saw both at one address.
	liarA := signed("aa:00:00:00:00:01", "198.51.100.1", "")
	liarB := signed("aa:00:00:00:00:02", "198.51.100.2", "")
	// Two containers on one kernel, without proofs.
	boxA := unsigned("aa:00:00:00:00:03", "192.0.2.10", "4c1f6f0e-0000-4000-8000-000000000001")
	boxB := unsigned("aa:00:00:00:00:04", "192.0.2.11", "4c1f6f0e-0000-4000-8000-000000000001")
	// No MAC on either: not one machine for that alone.
	bareA := unsigned("", "192.0.2.20", "")
	bareB := unsigned("", "192.0.2.21", "")

	hostKeys := hostKeysByPK(logging.MustGetLogger("bw-collect-test"), surveys, day)
	require.Equal(t, "ip:"+ipProofSignedIP, hostShared(hostKeys[liarA], hostKeys[liarB]))
	require.Equal(t, "boot:4c1f6f0e-0000-4000-8000-000000000001", hostShared(hostKeys[boxA], hostKeys[boxB]))
	require.Empty(t, hostShared(hostKeys[bareA], hostKeys[bareB]))
	require.Empty(t, hostShared(hostKeys[liarA], hostKeys[boxA]))
	// A survey with no proof and no version has no IP to share.
	require.NotContains(t, hostKeys[boxA], "ip:192.0.2.10")
}

// TestTransportsConfirmedNeedThePeer checks which visors bw-collect lists as
// having two transports: only transports the other edge reported too, that
// are not dmsg and not between two visors on one host, count.
func TestTransportsConfirmedNeedThePeer(t *testing.T) {
	const date = "2026-11-21"
	pk := func() string { p, _ := cipher.GenerateKeyPair(); return p.Hex() }
	x, y, z, w, v, u := pk(), pk(), pk(), pk(), pk(), pk()
	edge := func(sent, recv uint64) *tpdstore.EdgeBandwidth {
		return &tpdstore.EdgeBandwidth{Sent: sent, Recv: recv}
	}
	both := []tpdstore.DailyEdgeBandwidth{{Date: date, A: edge(50, 40), B: edge(40, 50)}}
	recs := []tpdstore.TransportMetric{
		{ID: "x-y", Type: "stcpr", Edges: []string{x, y}, Daily: both},
		{ID: "x-z", Type: "sudph", Edges: []string{x, z}, Daily: both},
		{ID: "w-y", Type: "stcpr", Edges: []string{w, y}, Daily: both},
		{ID: "w-z one edge", Type: "stcpr", Edges: []string{w, z}, Daily: []tpdstore.DailyEdgeBandwidth{{Date: date, A: edge(50, 0)}}},
		{ID: "w-v dmsg", Type: "dmsg", Edges: []string{w, v}, Daily: both},
		{ID: "w-u one host", Type: "stcpr", Edges: []string{w, u}, Daily: both},
		{ID: "w-z other day", Type: "stcpr", Edges: []string{w, z}, Daily: []tpdstore.DailyEdgeBandwidth{{Date: "2026-11-20", A: edge(5, 5), B: edge(5, 5)}}},
	}
	nonces, err := httpauth.NewNonceStore(context.Background(), storeconfig.Config{Type: storeconfig.Memory}, "")
	require.NoError(t, err)
	tpdAPI := api.New(logging.MustGetLogger("tpd-test"), &metricsRecordStore{recs: recs}, nonces, false, tpdmetrics.NewEmpty(), "", "")

	wd := t.TempDir()
	surveys := filepath.Join(wd, "log_backups")
	for _, p := range []string{w, u} {
		require.NoError(t, os.MkdirAll(filepath.Join(surveys, p), 0o750))
		require.NoError(t, os.WriteFile(filepath.Join(surveys, p, "node-info.json"), []byte(`{"ip_address":"203.0.113.9","skywire_version":"v1.3.112"}`), 0o600))
	}
	oldCache, oldHist, oldSurveys, oldDate, oldDays, oldNoCache := bwCacheFile, histPath, bwSurveyPath, bwDate, bwDays, noCache
	t.Cleanup(func() {
		bwCacheFile, histPath, bwSurveyPath, bwDate, bwDays, noCache = oldCache, oldHist, oldSurveys, oldDate, oldDays, oldNoCache
	})
	bwCacheFile, histPath = filepath.Join(wd, "cache.json"), filepath.Join(wd, "hist")
	bwSurveyPath, bwDate, bwDays, noCache = surveys, date, 2, true

	require.NoError(t, collectBandwidth(nil, logging.MustGetLogger("bw-collect-test"), func() tpdMetricsReader { return tpdLocalAPI{tpdAPI} }))
	got := transportsConfirmedRead(transportsConfirmedPath(histPath, date))
	require.Equal(t, map[string]struct{}{x: {}, y: {}}, got)
	require.Nil(t, transportsConfirmedRead(transportsConfirmedPath(histPath, "2026-11-22")))
}
