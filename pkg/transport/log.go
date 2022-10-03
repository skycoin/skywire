package transport

import (
	"bytes"
	"encoding/csv"
	"encoding/gob"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"github.com/skycoin/skywire-utilities/pkg/logging"
)

// CsvEntry represents a logging entry for csv for a given Transport.
type CsvEntry struct {
	TpID uuid.UUID `json:"tp_id"`
	// atomic requires 64-bit alignment for struct field access
	LogEntry
	TimeStamp time.Time `json:"time_stamp"` // TimeStamp should be time.RFC3339Nano formatted
}

// LogEntry represents a logging entry for a given Transport.
// The entry is updated every time a packet is received or sent.
type LogEntry struct {
	// atomic requires 64-bit alignment for struct field access
	RecvBytes uint64 `json:"recv"` // Total received bytes.
	SentBytes uint64 `json:"sent"` // Total sent bytes.
}

// AddRecv records read.
func (le *LogEntry) AddRecv(n uint64) {
	atomic.AddUint64(&le.RecvBytes, n)
}

// AddSent records write.
func (le *LogEntry) AddSent(n uint64) {
	atomic.AddUint64(&le.SentBytes, n)
}

// MarshalJSON implements json.Marshaller
func (le *LogEntry) MarshalJSON() ([]byte, error) {
	rb := strconv.FormatUint(atomic.LoadUint64(&le.RecvBytes), 10)
	sb := strconv.FormatUint(atomic.LoadUint64(&le.SentBytes), 10)
	return []byte(`{"recv":` + rb + `,"sent":` + sb + `}`), nil
}

// GobEncode implements gob.GobEncoder
func (le *LogEntry) GobEncode() ([]byte, error) {
	var b bytes.Buffer
	enc := gob.NewEncoder(&b)
	if err := enc.Encode(le.RecvBytes); err != nil {
		return nil, err
	}
	if err := enc.Encode(le.SentBytes); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// GobDecode implements gob.GobDecoder
func (le *LogEntry) GobDecode(b []byte) error {
	r := bytes.NewReader(b)
	dec := gob.NewDecoder(r)
	var rb uint64
	if err := dec.Decode(&rb); err != nil {
		return err
	}
	var sb uint64
	if err := dec.Decode(&sb); err != nil {
		return err
	}
	atomic.StoreUint64(&le.RecvBytes, rb)
	atomic.StoreUint64(&le.SentBytes, sb)
	return nil
}

// LogStore stores transport log entries.
type LogStore interface {
	Entry(id uuid.UUID) (*LogEntry, error)
	Record(id uuid.UUID, entry *LogEntry) error
}

type inMemoryTransportLogStore struct {
	entries map[uuid.UUID]*LogEntry
	mu      sync.Mutex
}

// InMemoryTransportLogStore implements in-memory TransportLogStore.
func InMemoryTransportLogStore() LogStore {
	return &inMemoryTransportLogStore{
		entries: make(map[uuid.UUID]*LogEntry),
	}
}

func (tls *inMemoryTransportLogStore) Entry(id uuid.UUID) (*LogEntry, error) {
	tls.mu.Lock()
	entry, ok := tls.entries[id]
	tls.mu.Unlock()
	if !ok {
		return entry, errors.New("transport log entry not found")
	}

	return entry, nil
}

func (tls *inMemoryTransportLogStore) Record(id uuid.UUID, entry *LogEntry) error {
	tls.mu.Lock()
	if tls.entries == nil {
		tls.entries = make(map[uuid.UUID]*LogEntry)
	}
	tls.entries[id] = entry
	tls.mu.Unlock()
	return nil
}

type fileTransportLogStore struct {
	dir string
	log *logging.Logger
}

// FileTransportLogStore implements file TransportLogStore.
func FileTransportLogStore(dir string) (LogStore, error) {
	if err := os.MkdirAll(dir, 0606); err != nil {
		return nil, err
	}
	log := logging.MustGetLogger("transport")
	return &fileTransportLogStore{dir, log}, nil
}

func (tls *fileTransportLogStore) Entry(id uuid.UUID) (*LogEntry, error) {
	f, err := os.Open(filepath.Join(tls.dir, fmt.Sprintf("%s.log", id)))
	if err != nil {
		return nil, fmt.Errorf("open: %w", err)
	}
	defer func() {
		if err := f.Close(); err != nil {
			tls.log.WithError(err).Warn("Failed to close file")
		}
	}()

	entry := &LogEntry{}
	if err := json.NewDecoder(f).Decode(entry); err != nil {
		return nil, fmt.Errorf("json: %w", err)
	}

	return entry, nil
}

func (tls *fileTransportLogStore) Record(id uuid.UUID, entry *LogEntry) error {
	cEntry := CsvEntry{
		TpID:      id,
		LogEntry:  *entry,
		TimeStamp: time.Now().UTC(),
	}

	return tls.writeJSONToCSV(cEntry)
}

func (tls *fileTransportLogStore) writeJSONToCSV(entry CsvEntry) error {
	today := time.Now().UTC().Format("2006-01-02")
	f, err := os.OpenFile(filepath.Join(tls.dir, fmt.Sprintf("%s.csv", today)), os.O_RDWR|os.O_CREATE, 0600)
	if err != nil {
		return fmt.Errorf("open: %w", err)
	}
	defer func() {
		if err := f.Close(); err != nil {
			tls.log.WithError(err).Warn("Failed to close file")
		}
	}()

	csvReader := csv.NewReader(f)
	data, err := csvReader.ReadAll()
	if err != nil {
		return err
	}

	readLogs, err := createCsvEntry(data)
	if err != nil {
		return err
	}

	var writeLogs []CsvEntry
	for _, log := range readLogs {
		if log.TpID == entry.TpID {
			writeLogs = append(writeLogs, entry)
			continue
		}
		writeLogs = append(writeLogs, log)
	}

	writer := csv.NewWriter(f)
	defer writer.Flush()

	header := []string{"tp_id", "recv", "sent", "time_stamp"}
	if err := writer.Write(header); err != nil {
		return err
	}

	for _, r := range writeLogs {
		var csvRow []string
		csvRow = append(csvRow, r.TpID.String(), fmt.Sprint(r.LogEntry.RecvBytes), fmt.Sprint(r.LogEntry.SentBytes), r.TimeStamp.String())
		if err := writer.Write(csvRow); err != nil {
			return err
		}
	}

	return nil
}

func createCsvEntry(data [][]string) ([]CsvEntry, error) {
	// convert csv lines to array of structs
	var csvEntries []CsvEntry
	for i, line := range data {
		if i > 0 { // omit header line
			var entry CsvEntry
			for j, field := range line {
				if j == 0 {
					tpID, err := uuid.Parse(field)
					if err != nil {
						return nil, err
					}
					entry.TpID = tpID
				} else if j == 1 {
					recvBytes, err := strconv.Atoi(field)
					if err != nil {
						return nil, err
					}
					entry.LogEntry.RecvBytes = uint64(recvBytes)
				} else if j == 2 {
					sentBytes, err := strconv.Atoi(field)
					if err != nil {
						return nil, err
					}
					entry.LogEntry.SentBytes = uint64(sentBytes)
				} else if j == 3 {
					date, err := time.Parse("2006-01-02 15:04:05.999999999 -0700 MST", field)
					if err != nil {
						return nil, err
					}
					entry.TimeStamp = date
				}
			}
			csvEntries = append(csvEntries, entry)
		}
	}
	return csvEntries, nil
}
