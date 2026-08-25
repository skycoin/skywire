package visor

import (
	"testing"

	"github.com/skycoin/skywire/pkg/cipher"
)

func TestClientPKFromLeafPath(t *testing.T) {
	const (
		serverHex = "02190003862c24f69e2cf47e1cf0efaa3dc1d866ba6a24067de34c363058212c73"
		clientHex = "02e40731f3ab6d11d31c466429297f4869f299a7821108409c5e36b840253e4ba7"
	)
	var wantPK cipher.PubKey
	if err := wantPK.Set(clientHex); err != nil {
		t.Fatalf("bad test client hex: %v", err)
	}

	tests := []struct {
		name   string
		path   string
		wantOK bool
		wantPK cipher.PubKey
	}{
		{
			name:   "valid entry leaf",
			path:   "clients-by-server/" + serverHex + "/" + clientHex + "/entry",
			wantOK: true,
			wantPK: wantPK,
		},
		{
			name:   "tombstone suffix rejected",
			path:   "clients-by-server/" + serverHex + "/" + clientHex + "/tombstone",
			wantOK: false,
		},
		{
			name:   "missing client segment",
			path:   "clients-by-server/" + serverHex + "/entry",
			wantOK: false,
		},
		{
			name:   "malformed client hex",
			path:   "clients-by-server/" + serverHex + "/nothex/entry",
			wantOK: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pk, ok := clientPKFromLeafPath(tt.path)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if ok && pk != tt.wantPK {
				t.Fatalf("pk = %s, want %s", pk, tt.wantPK)
			}
		})
	}
}
