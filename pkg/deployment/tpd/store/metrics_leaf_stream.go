// Package store pkg/deployment/tpd/store/metrics_leaf_stream.go c4-net-discovery
package store

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
)

// EachMetricRecord decodes a day leaf's parts, gzipped or raw JSON arrays of
// TransportMetric, one record at a time. A day is ~16 MB of JSON, and
// decoding it whole held that and the full slice at once for each day read.
func EachMetricRecord(parts [][]byte, fn func(*TransportMetric) error) error {
	return eachRecord(parts, fn)
}

// CountMetricRecords counts a day leaf's records, checking that the leaf is
// well formed JSON without decoding the records' fields.
func CountMetricRecords(parts [][]byte) (int, error) {
	n := 0
	err := eachRecord(parts, func(*struct{}) error {
		n++
		return nil
	})
	return n, err
}

func eachRecord[T any](parts [][]byte, fn func(*T) error) error {
	for _, part := range parts {
		if err := eachInPart(part, fn); err != nil {
			return err
		}
	}
	return nil
}

func eachInPart[T any](part []byte, fn func(*T) error) error {
	var r io.Reader = bytes.NewReader(part)
	if len(part) >= 2 && part[0] == 0x1f && part[1] == 0x8b {
		zr, err := gzip.NewReader(r)
		if err != nil {
			return err
		}
		defer zr.Close() //nolint:errcheck
		r = zr
	}
	dec := json.NewDecoder(r)
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	if d, ok := tok.(json.Delim); !ok || d != '[' {
		return fmt.Errorf("metrics leaf: want a JSON array, got %v", tok)
	}
	for dec.More() {
		// A fresh value each time: fn may keep the record's pointers.
		var rec T
		if err := dec.Decode(&rec); err != nil {
			return err
		}
		if err := fn(&rec); err != nil {
			return err
		}
	}
	if _, err := dec.Token(); err != nil {
		return err
	}
	// Reading to EOF rejects trailing data and makes gzip check its CRC.
	if tok, err := dec.Token(); err != io.EOF {
		if err == nil {
			err = fmt.Errorf("metrics leaf: unexpected %v after the array", tok)
		}
		return err
	}
	return nil
}
