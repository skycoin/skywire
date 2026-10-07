// Package api pkg/deployment/tpd/api/cxo_perkey_publisher_test.go c4-net-discovery
package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/skycoin/skywire/pkg/cxo/cxoutils"
	"github.com/skycoin/skywire/pkg/logging"
)

func newTestPerKeyPublisher(api *API, started time.Time) (*PerKeyCXOPublisher, *[]capturedPut) {
	var puts []capturedPut
	p := &PerKeyCXOPublisher{
		api:        api,
		log:        logging.MustGetLogger("tpd-cxo-perkey-pub-test"),
		transports: newCompletenessTracker(started),
	}
	p.putFn = func(path string, body []byte) error {
		puts = append(puts, capturedPut{path: path, body: append([]byte(nil), body...)})
		return nil
	}
	return p, &puts
}

func decodePerKey(t *testing.T, p capturedPut) PerKeyStats {
	t.Helper()
	var body PerKeyStats
	if err := json.Unmarshal(cxoutils.Gunzip(p.body), &body); err != nil {
		t.Fatalf("published body is not PerKeyStats JSON: %v", err)
	}
	return body
}

// The feed's keys are the HTTP endpoint's body, so a reader can serve one
// in place of the other.
func TestPerKeyStatsMatchesHTTPHandler(t *testing.T) {
	api := &API{transportsCache: testEntries(30)}
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)

	rec := httptest.NewRecorder()
	api.getAllTransportsPerKeyStats(rec, httptest.NewRequest(http.MethodGet, "/all-transports/per-key-stats", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("handler status = %d, want 200", rec.Code)
	}
	var handlerBody map[string]map[string]int
	if err := json.Unmarshal(rec.Body.Bytes(), &handlerBody); err != nil {
		t.Fatalf("handler body not JSON: %v", err)
	}

	p, puts := newTestPerKeyPublisher(api, now.Add(-time.Hour))
	p.publishOnce(context.Background(), now)
	if len(*puts) != 1 || (*puts)[0].path != PerKeyPath {
		t.Fatalf("want one put to %s, got %+v", PerKeyPath, *puts)
	}
	body := decodePerKey(t, (*puts)[0])
	if !reflect.DeepEqual(body.Keys, handlerBody) {
		t.Fatalf("published keys differ from the handler body:\n got %v\nwant %v", body.Keys, handlerBody)
	}
	if body.Total != 30 || !body.Complete || body.Confidence != ConfidenceSettled {
		t.Fatalf("stamp = total %d complete %v confidence %q, want 30/true/settled",
			body.Total, body.Complete, body.Confidence)
	}
	// Every transport has two edges.
	sum := 0
	for _, counts := range body.Keys {
		sum += counts["total"]
	}
	if sum != 2*body.Total {
		t.Fatalf("per-key totals sum to %d, want %d", sum, 2*body.Total)
	}
}

// After a TPD restart the registry refills from near zero; a per-key table
// read then is mostly visors with too few transports. It must not replace
// the complete one on the feed until the holdover runs out.
func TestPerKeyPublisherHoldsLastCompleteSample(t *testing.T) {
	api := &API{transportsCache: testEntries(1000)}
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	p, puts := newTestPerKeyPublisher(api, now.Add(-time.Hour))

	p.publishOnce(context.Background(), now)
	if len(*puts) != 1 {
		t.Fatalf("settled sample should publish, got %d puts", len(*puts))
	}

	api.transportsCache = testEntries(300)
	for i := 0; i < 3; i++ {
		now = now.Add(perKeyPublishInterval)
		p.publishOnce(context.Background(), now)
	}
	if len(*puts) != 1 {
		t.Fatalf("partial samples must not overwrite the held complete one; got %d puts", len(*puts))
	}

	now = now.Add(statsIncompleteHoldover)
	p.publishOnce(context.Background(), now)
	if len(*puts) != 2 {
		t.Fatalf("after the holdover the partial sample should publish; got %d puts", len(*puts))
	}
	if body := decodePerKey(t, (*puts)[1]); body.Complete || body.Confidence != ConfidenceRefilling || body.Total != 300 {
		t.Fatalf("held-over sample = complete %v confidence %q total %d, want false/refilling/300",
			body.Complete, body.Confidence, body.Total)
	}
}

// An empty registry publishes nothing, as the HTTP endpoint answers 404.
func TestPerKeyPublisherSkipsEmptySet(t *testing.T) {
	api := &API{transportsCache: testEntries(0)}
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	p, puts := newTestPerKeyPublisher(api, now.Add(-time.Hour))
	p.publishOnce(context.Background(), now)
	if len(*puts) != 0 {
		t.Fatalf("empty set should publish nothing, got %d puts", len(*puts))
	}
}
