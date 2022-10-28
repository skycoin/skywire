// Package transport pkg/transport/log.go
package transport

import (
	"bytes"
	"context"
	"encoding/gob"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gocarina/gocsv"
	"github.com/google/uuid"

	"github.com/skycoin/skywire-utilities/pkg/logging"
)

const dateFormat string = "2006-01-02"

// CsvEntry represents a logging entry for csv for a given Transport.
type CsvEntry struct {
	TpID uuid.UUID `csv:"tp_id"`
	// atomic requires 64-bit alignment for struct field access
	LogEntry
	TimeStamp int64 `csv:"time_stamp"` // TimeStamp should be time.RFC3339Nano formatted
}

// LogEntry represents a logging entry for a given Transport.
// The entry is updated every time a packet is received or sent.
type LogEntry struct {
	// atomic requires 64-bit alignment for struct field access
	RecvBytes *uint64 `csv:"recv"` // Total received bytes.
	SentBytes *uint64 `csv:"sent"` // Total sent bytes.
}

// MakeLogEntry makes a new LogEntry by adding the info from old entry if found
func MakeLogEntry(ls LogStore, tpID uuid.UUID, log *logging.Logger) *LogEntry {
	oldLogEntry, err := ls.Entry(tpID)
	if err != nil {
		log.Warn(err)
		return &LogEntry{}
	}
	newEntry := NewLogEntry()
	if oldLogEntry != nil {
		newEntry.AddRecv(*oldLogEntry.RecvBytes)
		newEntry.AddSent(*oldLogEntry.SentBytes)
	}
	return newEntry
}

// NewLogEntry creates a new LogEntry
func NewLogEntry() *LogEntry {
	recv := uint64(0)
	sent := uint64(0)
	return &LogEntry{
		RecvBytes: &recv,
		SentBytes: &sent,
	}
}

// AddRecv records read.
func (le *LogEntry) AddRecv(n uint64) {
	atomic.AddUint64(le.RecvBytes, n)
}

// AddSent records write.
func (le *LogEntry) AddSent(n uint64) {
	atomic.AddUint64(le.SentBytes, n)
}

// Reset resets LogEntry.
func (le *LogEntry) Reset() {
	atomic.AddUint64(le.SentBytes, -*le.SentBytes)
	atomic.AddUint64(le.RecvBytes, -*le.RecvBytes)
}

// MarshalJSON implements json.Marshaller
func (le *LogEntry) MarshalJSON() ([]byte, error) {
	var rb uint64
	var sb uint64
	if le.RecvBytes != nil {
		rb = atomic.LoadUint64(le.RecvBytes)
	}
	if le.SentBytes != nil {
		sb = atomic.LoadUint64(le.SentBytes)
	}
	return []byte(`{"recv":` + fmt.Sprint(rb) + `,"sent":` + fmt.Sprint(sb) + `}`), nil
}

// GobEncode implements gob.GobEncoder
func (le *LogEntry) GobEncode() ([]byte, error) {
	var b bytes.Buffer
	enc := gob.NewEncoder(&b)
	if le.RecvBytes != nil {
		rb := atomic.LoadUint64(le.RecvBytes)
		if err := enc.Encode(rb); err != nil {
			return nil, err
		}
	}
	if le.SentBytes != nil {
		sb := atomic.LoadUint64(le.SentBytes)
		if err := enc.Encode(sb); err != nil {
			return nil, err
		}
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
	if le.RecvBytes != nil {
		atomic.StoreUint64(le.RecvBytes, rb)
	}
	if le.SentBytes != nil {
		atomic.StoreUint64(le.SentBytes, sb)
	}
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
	dir      string
	log      *logging.Logger
	mu       sync.Mutex
	fileName string
}

// FileTransportLogStore implements file TransportLogStore.
func FileTransportLogStore(ctx context.Context, dir string, rInterval time.Duration, log *logging.Logger) (LogStore, error) {
	if err := os.MkdirAll(dir, 0644); err != nil {
		return nil, err
	}

	fLogStore := &fileTransportLogStore{
		dir: dir,
		log: log,
	}

	go func() {
		ticker := time.NewTicker(time.Hour * 5)
		defer ticker.Stop()
		fLogStore.cleanLogs(rInterval)
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				fLogStore.cleanLogs(rInterval)
			}
		}
	}()

	return fLogStore, nil
}

func (tls *fileTransportLogStore) Entry(tpID uuid.UUID) (*LogEntry, error) {
	tls.mu.Lock()
	defer tls.mu.Unlock()

	entries, err := tls.readFromCSV(tls.todayFileName())
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if entry.TpID == tpID {
			return &entry.LogEntry, nil
		}
	}
	return nil, nil
}

func (tls *fileTransportLogStore) Record(tpID uuid.UUID, lEntry *LogEntry) error {
	tls.mu.Lock()
	defer tls.mu.Unlock()

	cEntry := &CsvEntry{
		TpID:      tpID,
		LogEntry:  *lEntry,
		TimeStamp: time.Now().UTC().Unix(),
	}

	return tls.writeToCSV(cEntry)
}

func (tls *fileTransportLogStore) writeToCSV(cEntry *CsvEntry) error {

	today := tls.todayFileName()
	// we check if the date of the file has changed or not
	// if it is then it means it's a new day so we need to reset the LogEntry
	// so that we can start the count again for the new day and file
	if tls.fileName != "" && tls.fileName != tls.todayFileName() {
		// before we reset we need to save the current data so we save it in the previous days file
		// note: the timestamp of this entry will likely be of the current day so if a log file has
		// a timestamp of next day then it is an indicator that it's an inter-day transport log
		today = tls.fileName
	}

	f, err := os.OpenFile(filepath.Join(tls.dir, today), os.O_RDWR|os.O_CREATE, 0644) //nolint
	if err != nil {
		return err
	}

	defer func() {
		if err := f.Close(); err != nil {
			tls.log.WithError(err).Errorln("Failed to close csv file")
		}
	}()

	readClients := []*CsvEntry{}
	writeClients := []*CsvEntry{}

	if err := gocsv.UnmarshalFile(f, &readClients); err != nil && !errors.Is(err, gocsv.ErrEmptyCSVFile) { // Load clients from file
		return err
	}

	var update bool
	for _, client := range readClients {
		// update if readClients contains the cEntry
		if client.TpID == cEntry.TpID {
			writeClients = append(writeClients, cEntry)
			update = true
			continue
		}
		writeClients = append(writeClients, client)
	}

	// write when the readClients are does not contain cEntry
	if !update {
		writeClients = append(writeClients, cEntry)
	}

	if _, err := f.Seek(0, 0); err != nil { // Go to the start of the file
		return err
	}

	err = gocsv.MarshalFile(&writeClients, f) // Use this to save the CSV back to the file
	if err != nil {
		return err
	}

	// we reset the entry after it is saved
	if tls.fileName != "" && tls.fileName != tls.todayFileName() {
		cEntry.LogEntry.Reset()
	}

	tls.fileName = tls.todayFileName()

	return nil
}

func (tls *fileTransportLogStore) readFromCSV(fileName string) ([]*CsvEntry, error) {
	f, err := os.OpenFile(filepath.Join(tls.dir, fmt.Sprint(fileName)), os.O_RDWR|os.O_CREATE, 0644) //nolint
	if err != nil {
		return nil, err
	}

	defer func() {
		if err := f.Close(); err != nil {
			tls.log.WithError(err).Errorln("Failed to close csv file")
		}
	}()

	readClients := []*CsvEntry{}

	if err := gocsv.UnmarshalFile(f, &readClients); err != nil && !errors.Is(err, gocsv.ErrEmptyCSVFile) { // Load clients from file
		return nil, err
	}
	return readClients, nil
}

// CleanLogs cleans the logs that are older than the given log rotation interval
func (tls *fileTransportLogStore) cleanLogs(rInterval time.Duration) {

	files, err := os.ReadDir(tls.dir)
	if err != nil {
		tls.log.Warn(err)
	}

	for _, file := range files {
		if !file.IsDir() {
			interval := time.Now().UTC().Add(-rInterval)
			date, err := time.Parse(dateFormat, strings.ReplaceAll(file.Name(), ".csv", ""))
			if err != nil {
				tls.log.Warn(err)
			}
			if date.Before(interval) {
				err = os.Remove(tls.dir + "/" + file.Name())
				if err != nil {
					tls.log.Warn(err)
				}
				tls.log.Debugf("transport log file cleaned: %v", file.Name())
			}
		}
	}
}

func (tls *fileTransportLogStore) todayFileName() string {
	return fmt.Sprintf("%s.csv", time.Now().UTC().Format(dateFormat))
}
