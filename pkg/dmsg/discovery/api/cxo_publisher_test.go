// Package api — cxo_publisher_test.go covers the heartbeat
// short-circuit on PublishSetEntry. Pins the equality semantics so a
// future field added to disc.Entry has to be explicitly classified
// as either materially-visible (include in the comparison + skip
// expected) or heartbeat-only (exclude).
package api

import (
	"testing"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
)

func TestEntryContentEqual(t *testing.T) {
	pkClient, _ := cipher.GenerateKeyPair()
	pkS1, _ := cipher.GenerateKeyPair()
	pkS2, _ := cipher.GenerateKeyPair()
	pkExtra, _ := cipher.GenerateKeyPair()
	pkOther, _ := cipher.GenerateKeyPair()

	baseClient := func() *disc.Entry {
		return &disc.Entry{
			Version:    "0.0.1",
			Sequence:   42,
			Timestamp:  1700000000,
			Static:     pkClient,
			Client:     &disc.Client{DelegatedServers: []cipher.PubKey{pkS1, pkS2}},
			ClientType: "visor",
			Signature:  "abc",
		}
	}

	cases := []struct {
		name string
		a, b func() *disc.Entry
		want bool
	}{
		{
			name: "identical",
			a:    baseClient,
			b:    baseClient,
			want: true,
		},
		{
			name: "heartbeat — only Sequence/Timestamp/Signature differ",
			a:    baseClient,
			b: func() *disc.Entry {
				e := baseClient()
				e.Sequence = 43
				e.Timestamp = 1700000060
				e.Signature = "def"
				return e
			},
			want: true,
		},
		{
			name: "DelegatedServers added",
			a:    baseClient,
			b: func() *disc.Entry {
				e := baseClient()
				e.Client.DelegatedServers = append(e.Client.DelegatedServers, pkExtra)
				return e
			},
			want: false,
		},
		{
			name: "DelegatedServers removed",
			a:    baseClient,
			b: func() *disc.Entry {
				e := baseClient()
				e.Client.DelegatedServers = []cipher.PubKey{pkS1}
				return e
			},
			want: false,
		},
		{
			name: "DelegatedServers reordered — same set",
			// Set semantics, not slice: order shouldn't matter
			// because the publish fan-out is per-server.
			a: baseClient,
			b: func() *disc.Entry {
				e := baseClient()
				e.Client.DelegatedServers = []cipher.PubKey{pkS2, pkS1}
				return e
			},
			want: true,
		},
		{
			name: "ClientType changed",
			a:    baseClient,
			b: func() *disc.Entry {
				e := baseClient()
				e.ClientType = "skysocks"
				return e
			},
			want: false,
		},
		{
			name: "Version changed",
			a:    baseClient,
			b: func() *disc.Entry {
				e := baseClient()
				e.Version = "0.0.2"
				return e
			},
			want: false,
		},
		{
			name: "Protocol changed",
			a:    baseClient,
			b: func() *disc.Entry {
				e := baseClient()
				e.Protocol = "noise"
				return e
			},
			want: false,
		},
		{
			name: "Static PK changed",
			// Defensive — caller-provided oldEntry/newEntry should
			// share Static, but the helper must not silently treat
			// distinct PKs as equal.
			a: baseClient,
			b: func() *disc.Entry {
				e := baseClient()
				e.Static = pkOther
				return e
			},
			want: false,
		},
		{
			name: "Client absent on one side",
			a:    baseClient,
			b: func() *disc.Entry {
				e := baseClient()
				e.Client = nil
				return e
			},
			want: false,
		},
		{
			name: "nil a",
			a:    func() *disc.Entry { return nil },
			b:    baseClient,
			want: false,
		},
		{
			name: "nil b",
			a:    baseClient,
			b:    func() *disc.Entry { return nil },
			want: false,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := entryContentEqual(c.a(), c.b())
			if got != c.want {
				t.Fatalf("entryContentEqual: got %v, want %v", got, c.want)
			}
		})
	}
}
