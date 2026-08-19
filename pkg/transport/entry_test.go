// Package transport_test pkg/transport/entry_test.go
package transport_test

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/transport"
)

func TestNewEntry(t *testing.T) {
	pkA, _ := cipher.GenerateKeyPair()
	pkB, _ := cipher.GenerateKeyPair()

	entryAB := transport.MakeEntry(pkA, pkB, "", transport.LabelUser)
	entryBA := transport.MakeEntry(pkA, pkB, "", transport.LabelUser)

	assert.True(t, entryAB.Edges == entryBA.Edges)
	assert.True(t, entryAB.ID == entryBA.ID)
	assert.NotNil(t, entryAB.ID)
	assert.NotNil(t, entryBA.ID)
}

// TestCompactEntryRoundTrip verifies the compact discovery form
// reconstructs a byte-identical full Entry from (reporter, remote, type)
// regardless of which edge the reporter is, and that the default
// LabelAutomatic is elided-and-restored losslessly.
func TestCompactEntryRoundTrip(t *testing.T) {
	reporter, _ := cipher.GenerateKeyPair()
	remote, _ := cipher.GenerateKeyPair()

	for _, label := range []transport.Label{
		transport.LabelAutomatic, transport.LabelUser, transport.LabelSkycoin, transport.LabelSetup,
	} {
		full := transport.MakeEntry(reporter, remote, "stcpr", label)
		ce := full.ToCompact(reporter)

		// The reporter's own PK is never present in the compact row.
		assert.Equal(t, remote, ce.Remote, "compact keeps only the remote edge")
		if label == transport.LabelAutomatic {
			assert.Equal(t, transport.Label(""), ce.Label, "default label is elided")
		}

		got := transport.EntryFromCompact(reporter, ce)
		assert.Equal(t, full.ID, got.ID, "ID recomputes identically (label=%s)", label)
		assert.Equal(t, full.Edges, got.Edges, "edges canonicalize identically (label=%s)", label)
		assert.Equal(t, full.Type, got.Type)
		assert.Equal(t, label, got.Label, "label restored (label=%s)", label)
	}

	// Reconstruction is independent of which edge sorts first: build the
	// compact row from the perspective of BOTH edges and confirm the same
	// canonical entry comes back.
	full := transport.MakeEntry(reporter, remote, "squicr", transport.LabelAutomatic)
	fromReporter := transport.EntryFromCompact(reporter, full.ToCompact(reporter))
	fromRemote := transport.EntryFromCompact(remote, full.ToCompact(remote))
	assert.Equal(t, fromReporter.ID, fromRemote.ID)
	assert.Equal(t, fromReporter.Edges, fromRemote.Edges)
}

func ExampleSignedEntry_Sign() {
	pkA, skA := cipher.GenerateKeyPair()
	pkB, skB := cipher.GenerateKeyPair()

	entry := transport.MakeEntry(pkA, pkB, "mock", transport.LabelUser)
	sEntry := &transport.SignedEntry{Entry: &entry}

	if sEntry.Signatures[0].Null() && sEntry.Signatures[1].Null() {
		fmt.Println("No signatures set")
	}

	if err := sEntry.Sign(pkA, skA); err != nil {
		fmt.Println("error signing with skA: ", err)
	}

	if (!sEntry.Signatures[0].Null() && sEntry.Signatures[1].Null()) ||
		(!sEntry.Signatures[1].Null() && sEntry.Signatures[0].Null()) {
		fmt.Println("One signature set")
	}

	if err := sEntry.Sign(pkB, skB); err != nil {
		fmt.Println("error signing with skB: ", err)
	}

	if !sEntry.Signatures[0].Null() && !sEntry.Signatures[1].Null() {
		fmt.Println("Both signatures set")
	} else {
		fmt.Printf("sEntry.Signatures:\n%v\n", sEntry.Signatures)
	}

	// Output: No signatures set
	// One signature set
	// Both signatures set
}

func ExampleSignedEntry_Signature() {
	pkA, skA := cipher.GenerateKeyPair()
	pkB, skB := cipher.GenerateKeyPair()

	entry := transport.MakeEntry(pkA, pkB, "mock", transport.LabelUser)
	sEntry := &transport.SignedEntry{Entry: &entry}

	if err := sEntry.Sign(pkA, skA); err != nil {
		fmt.Println("Error signing sEntry with (pkA,skA)")
	}

	if err := sEntry.Sign(pkB, skB); err != nil {
		fmt.Println("Error signing sEntry with (pkB,skB)")
	}

	idxA := sEntry.Entry.EdgeIndex(pkA)
	idxB := sEntry.Entry.EdgeIndex(pkB)

	sigA, errA := sEntry.Signature(pkA)
	sigB, errB := sEntry.Signature(pkB)

	if errA == nil && sigA == sEntry.Signatures[idxA] {
		fmt.Println("SignatureA got")
	}

	if errB == nil && (sigB == sEntry.Signatures[idxB]) {
		fmt.Println("SignatureB got")
	}

	// Incorrect case
	pkC, _ := cipher.GenerateKeyPair()
	if _, err := sEntry.Signature(pkC); err != nil {
		fmt.Printf("SignatureC got error: %v\n", err)
	}

	//
	// Output: SignatureA got
	// SignatureB got
	// SignatureC got error: edge index not found
}
