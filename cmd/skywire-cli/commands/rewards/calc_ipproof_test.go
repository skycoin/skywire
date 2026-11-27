// Package clirewards cmd/skywire-cli/commands/rewards/calc_ipproof_test.go c4-vis-cli
package clirewards

import (
	"encoding/json"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
)

// ipProofSignedIP is the IP the test servers see the visor at.
const ipProofSignedIP = "5.6.7.8"

// ipProofSign builds the proof a dmsg server with srvSK gives the visor visorSK
// when it sees the visor at ipProofSignedIP at time at.
func ipProofSign(t *testing.T, visorPK cipher.PubKey, visorSK cipher.SecKey, srvPK cipher.PubKey, srvSK cipher.SecKey, at time.Time) *dmsg.IPProof {
	t.Helper()
	req := dmsg.StreamRequest{
		Timestamp: at.UnixNano(),
		SrcAddr:   dmsg.Addr{PK: visorPK, Port: 49152},
		DstAddr:   dmsg.Addr{PK: srvPK, Port: 1},
		IPinfo:    true,
	}
	reqObj, err := dmsg.MakeSignedStreamRequest(&req, visorSK)
	if err != nil {
		t.Fatal(err)
	}
	resp := dmsg.StreamResponse{ReqHash: reqObj.Hash(), Accepted: true, IP: net.ParseIP(ipProofSignedIP), Timestamp: at.UnixNano()}
	respObj, err := dmsg.MakeSignedStreamResponse(&resp, srvSK)
	if err != nil {
		t.Fatal(err)
	}
	return &dmsg.IPProof{Request: reqObj, Response: respObj}
}

func TestSurveyIPUsesTheSignedIP(t *testing.T) {
	visorPK, visorSK := cipher.GenerateKeyPair()
	otherPK, otherSK := cipher.GenerateKeyPair()
	srvPK, srvSK := cipher.GenerateKeyPair()
	strangerPK, strangerSK := cipher.GenerateKeyPair()
	known := ipProofServerKnown
	ipProofServerKnown = func(pk cipher.PubKey) bool { return pk == srvPK }
	t.Cleanup(func() { ipProofServerKnown = known })

	day := time.Date(2026, time.December, 1, 0, 0, 0, 0, time.UTC)
	const newVersion = "v1.3.114"
	pk := visorPK.Hex()

	cases := []struct {
		name    string
		version string
		proof   *dmsg.IPProof
		day     time.Time
		wantIP  string
		wantErr error
	}{
		{"signed IP wins over the reported one", newVersion, ipProofSign(t, visorPK, visorSK, srvPK, srvSK, day), day, "5.6.7.8", nil},
		{"no proof from a version that sends one", newVersion, nil, day, "", errIPProofMissing},
		{"no proof from a release before proofs keeps the reported IP", "v1.3.113", nil, day, "1.2.3.4", nil},
		{"a dev build of that release keeps it too", "v1.3.113-26-g327848978", nil, day, "1.2.3.4", nil},
		{"no proof and no version", "", nil, day, "", errIPProofMissing},
		{"no proof and a version that does not parse", "latest", nil, day, "", errIPProofMissing},
		{"a bad proof from an old release keeps the reported IP", "v1.3.112", ipProofSign(t, otherPK, otherSK, srvPK, srvSK, day), day, "1.2.3.4", nil},
		{"another visor's proof", newVersion, ipProofSign(t, otherPK, otherSK, srvPK, srvSK, day), day, "", errIPProofOtherKey},
		{"a server outside the deployment", newVersion, ipProofSign(t, visorPK, visorSK, strangerPK, strangerSK, day), day, "", errIPProofUnknownServer},
		{"a proof older than the window", newVersion, ipProofSign(t, visorPK, visorSK, srvPK, srvSK, day.Add(-ipProofMaxAge-time.Hour)), day, "", errIPProofStale},
		{"a newer proof for an older day", newVersion, ipProofSign(t, visorPK, visorSK, srvPK, srvSK, day.AddDate(0, 0, 10)), day, "5.6.7.8", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ip, err := surveyIP(&surveyData{IPAddr: "1.2.3.4", IPProof: c.proof, SkywireVersion: c.version}, pk, c.day)
			if !errors.Is(err, c.wantErr) || ip != c.wantIP {
				t.Fatalf("got %q, %v; want %q, %v", ip, err, c.wantIP, c.wantErr)
			}
		})
	}
}

func TestSurveyIPRejectsAForgedIP(t *testing.T) {
	visorPK, visorSK := cipher.GenerateKeyPair()
	srvPK, srvSK := cipher.GenerateKeyPair()
	known := ipProofServerKnown
	ipProofServerKnown = func(pk cipher.PubKey) bool { return pk == srvPK }
	t.Cleanup(func() { ipProofServerKnown = known })

	day := time.Date(2026, time.December, 1, 0, 0, 0, 0, time.UTC)
	proof := ipProofSign(t, visorPK, visorSK, srvPK, srvSK, day)
	resp, err := proof.Response.ObtainStreamResponse()
	if err != nil {
		t.Fatal(err)
	}
	resp.IP = net.ParseIP("9.9.9.9")
	forged, err := dmsg.MakeSignedStreamResponse(&resp, visorSK)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := surveyIP(&surveyData{IPProof: &dmsg.IPProof{Request: proof.Request, Response: forged}}, visorPK.Hex(), day); err == nil {
		t.Fatal("a response the visor signed itself must not verify")
	}
}

func TestSurveyIPProofSurvivesTheSurveyFile(t *testing.T) {
	visorPK, visorSK := cipher.GenerateKeyPair()
	srvPK, srvSK := cipher.GenerateKeyPair()
	known := ipProofServerKnown
	ipProofServerKnown = func(pk cipher.PubKey) bool { return pk == srvPK }
	t.Cleanup(func() { ipProofServerKnown = known })

	day := time.Date(2026, time.December, 1, 0, 0, 0, 0, time.UTC)
	body, err := json.MarshalIndent(map[string]any{"ip_address": "1.2.3.4", "ip_proof": ipProofSign(t, visorPK, visorSK, srvPK, srvSK, day)}, "", "\t")
	if err != nil {
		t.Fatal(err)
	}
	s, err := parseSurveyBytes(body)
	if err != nil {
		t.Fatal(err)
	}
	if ip, err := surveyIP(s, visorPK.Hex(), day); err != nil || ip != "5.6.7.8" {
		t.Fatalf("got %q, %v; want 5.6.7.8", ip, err)
	}
}
