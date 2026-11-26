// Package clirewards cmd/skywire-cli/commands/rewards/bwcollect_host_test.go c4-vis-cli
package clirewards

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	deprewards "github.com/skycoin/skywire/pkg/deployment/rewards"
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

	day := deprewards.IPProofCutoff.AddDate(0, 0, 1)
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
	// Past the cutoff a survey with no proof has no IP to share.
	require.NotContains(t, hostKeys[boxA], "ip:192.0.2.10")
}
