// Package dmsgasync pkg/dmsg/dmsgasync/discovery.go c1-net-dmsg
package dmsgasync

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
)

// ErrNoRecord means dmsg discovery has no inbox record for the key.
var ErrNoRecord = errors.New("dmsg-async: no inbox record")

func inboxURL(discURL string, pk cipher.PubKey) string {
	return strings.TrimSuffix(discURL, "/") + "/dmsg-discovery/inbox/" + pk.Hex()
}

// PublishRecord posts rec to the dmsg discovery at discURL.
func PublishRecord(ctx context.Context, hc *http.Client, discURL string, rec Record) error {
	body, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, inboxURL(discURL, rec.PK), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 1024)) //nolint:errcheck
		return fmt.Errorf("dmsg-async: publish record: %s: %s", resp.Status, bytes.TrimSpace(msg))
	}
	return nil
}

// FetchRecord reads pk's inbox record from the dmsg discovery at discURL and
// verifies it.
func FetchRecord(ctx context.Context, hc *http.Client, discURL string, pk cipher.PubKey) (Record, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, inboxURL(discURL, pk), nil)
	if err != nil {
		return Record{}, err
	}
	resp, err := hc.Do(req)
	if err != nil {
		return Record{}, err
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode == http.StatusNotFound {
		return Record{}, ErrNoRecord
	}
	if resp.StatusCode != http.StatusOK {
		return Record{}, fmt.Errorf("dmsg-async: fetch record: %s", resp.Status)
	}
	var rec Record
	if err := json.NewDecoder(io.LimitReader(resp.Body, controlMax)).Decode(&rec); err != nil {
		return Record{}, fmt.Errorf("%w: %v", ErrBadRecord, err)
	}
	if rec.PK != pk {
		return Record{}, fmt.Errorf("%w: record is for another key", ErrBadRecord)
	}
	return rec, rec.Verify(time.Now())
}
