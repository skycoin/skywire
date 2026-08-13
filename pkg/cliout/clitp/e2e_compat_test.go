package clitp

import (
	"encoding/json"
	"testing"
)

// stcpTpView is internal/integration's private copy of this shape. If the
// canonical type stops filling these fields, the e2e suite silently reads
// zeros instead of failing to compile — which is the whole reason the type
// moved here. This asserts the two still agree.
type stcpTpView struct {
	ID        string `json:"id"`
	Type      string `json:"type"`
	Remote    string `json:"remote_pk"`
	RecvBytes uint64 `json:"recv_bytes"`
	SentBytes uint64 `json:"sent_bytes"`
}

func TestE2EViewStillDecodes(t *testing.T) {
	src := Transport{TpMode: "out", RecvBytes: 42, SentBytes: 7}
	b, err := json.Marshal(Transports{src})
	if err != nil {
		t.Fatal(err)
	}
	var got []stcpTpView
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].RecvBytes != 42 || got[0].SentBytes != 7 {
		t.Fatalf("e2e's view no longer decodes this shape: %+v", got)
	}
}
