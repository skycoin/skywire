package transport_test

import (
	"fmt"
	"testing"

	"github.com/skycoin/dmsg/cipher"
	"github.com/stretchr/testify/assert"

	"github.com/skycoin/skywire/pkg/transport"
)

func TestNewEntry(t *testing.T) {
	pkA, _ := cipher.GenerateKeyPair()
	pkB, _ := cipher.GenerateKeyPair()

	entryAB := transport.MakeEntry(pkA, pkB, "", true, transport.LabelUser)
	entryBA := transport.MakeEntry(pkA, pkB, "", true, transport.LabelUser)

	assert.True(t, entryAB.Edges == entryBA.Edges)
	assert.True(t, entryAB.ID == entryBA.ID)
	assert.NotNil(t, entryAB.ID)
	assert.NotNil(t, entryBA.ID)
}

func ExampleSignedEntry_Sign() {
	pkA, skA := cipher.GenerateKeyPair()
	pkB, skB := cipher.GenerateKeyPair()

	entry := transport.MakeEntry(pkA, pkB, "mock", true, transport.LabelUser)
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

	entry := transport.MakeEntry(pkA, pkB, "mock", true, transport.LabelUser)
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
